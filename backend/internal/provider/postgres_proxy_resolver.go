// 包 provider — ProxyResolver 的 PostgreSQL 后端实现。
// 代理资源从 provider_accounts.proxy_url 单字符串列迁入 tenant-scoped
// proxies 独立表; provider_accounts 通过
// proxy_id FK 关联。本文件 JOIN proxies 表取出字段在 Go 端用 url.URL{} +
// url.UserPassword() 构造代理 URL, 避免 SQL 字符串拼接破坏 URL 转义。
//
// 失效语义 (fail-closed, 不绕过代理):
//   - account.proxy_id IS NULL                                   → 直连 (admin 显式选无代理)
//   - account.proxy_id 指向 proxies 且 status='active' + 未软删   → 该代理 URL
//   - account.proxy_id 指向 proxies 但 status != 'active' / 软删 → ErrProxyUnhealthy
//     (调用方应拒绝出站, 强制
//     admin 介入. 不静默走直连,
//     避免破坏账号级 IP 隔离)
//   - account 行不存在                                            → ErrAccountNotFound
//   - URL 重建后格式错误                                          → ErrProxyURLInvalid
//   - DB 故障                                                    → 包装底层 pgx 错误
package provider

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"net/url"
	"strconv"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/proxysecret"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrProxyURLInvalid 表示从 proxies 表字段在 Go 端构造的 URL 经 net/url.Parse
// 二次校验时无法识别 (例如 host 含非法字符)。包装底层错误。
var ErrProxyURLInvalid = errors.New("provider proxy resolver: 构造的代理 URL 格式无效")

// ErrProxyUnhealthy 表示 account 绑定了 proxy_id 但代理当前 status != 'active'
// 或已软删. fail-closed 信号: 调用方 (dispatcher) 应拒绝该出站, 强制 admin 重新
// 选健康代理. 不能静默走直连, 否则破坏账号级 IP 隔离 (例如反检测设计的"该账号
// 永远从住宅 IP 出"约定被绕过).
var ErrProxyUnhealthy = errors.New("provider proxy resolver: 绑定的代理已 disabled/dead 或软删")

// PostgresProxyResolver 是基于 PostgreSQL 的 ProxyResolver 实现。
// 通过 provider_accounts.proxy_id 一次 LEFT JOIN proxies 表取出字段,
// Go 端用 url.UserPassword() 转义后构造 URL。
type PostgresProxyResolver struct {
	pool *pgxpool.Pool
	keys credentialstore.KeyProvider
	// directFallbackAllowed is the platform kill-switch getter (nil = disallowed).
	// When it returns true, a proxy with fallback_mode='direct' may fall through to
	// direct egress on unhealthy; otherwise unhealthy stays fail-closed (reject).
	directFallbackAllowed func() bool
}

// 编译期接口合规断言。
var _ ProxyResolver = (*PostgresProxyResolver)(nil)

// NewPostgresProxyResolver 用给定的连接池创建 PostgresProxyResolver。
func NewPostgresProxyResolver(pool *pgxpool.Pool) *PostgresProxyResolver {
	return &PostgresProxyResolver{pool: pool}
}

func NewPostgresProxyResolverWithKeys(pool *pgxpool.Pool, keys credentialstore.KeyProvider) *PostgresProxyResolver {
	return &PostgresProxyResolver{pool: pool, keys: keys}
}

// WithDirectFallbackGate sets the platform kill-switch getter used to authorize
// fallback_mode='direct'. Returns the receiver for chaining. A nil getter (the
// default) keeps direct fallback disabled, preserving fail-closed behavior.
func (r *PostgresProxyResolver) WithDirectFallbackGate(allowed func() bool) *PostgresProxyResolver {
	if r != nil {
		r.directFallbackAllowed = allowed
	}
	return r
}

// proxyRow 是 SQL JOIN 返回的中间值, 跟 Go *url.URL 构造解耦.
type proxyRow struct {
	tenantID       int64
	proxyGroupID   string // PROXY-05: 账号绑定的代理组 (空=未绑组)
	accountExists  bool
	hasProxyBound  bool   // account.proxy_id IS NOT NULL
	proxyIsHealthy bool   // 绑定的代理同时满足 status='active' + 未软删
	protocol       string // 仅 healthy=true 时有效
	host           string
	port           int
	username       *string
	secret         *string
	fallbackMode   string // PROXY fallback: 'reject'(默认/fail-closed) | 'direct'(需平台总闸同开)
	// tenant-default tier (PROXY-03): 账号无绑定时回退到 tenant.default_proxy_id
	hasDefaultBound  bool
	defaultIsHealthy bool
	dprotocol        string
	dhost            string
	dport            int
	dusername        *string
	dsecret          *string
	dFallbackMode    string // tenant-default 层代理的 fallback_mode
}

// Resolve 按 accountID 查 provider_accounts JOIN proxies 取出代理字段在 Go 端
// 构造 URL.
//
// 返回值语义:
//   - (*url.URL, nil)            : 代理健康, URL 重建成功
//   - (nil, nil)                 : account.proxy_id IS NULL (admin 显式直连)
//   - (nil, ErrAccountNotFound)  : account 不存在
//   - (nil, ErrProxyUnhealthy)   : account 绑了 proxy_id 但 proxy unhealthy/软删
//   - (nil, ErrProxyURLInvalid)  : 构造 URL 后 net/url.Parse 校验失败
//   - (nil, 其它 error)          : DB 基础设施故障
func (r *PostgresProxyResolver) Resolve(ctx context.Context, accountID int64) (*url.URL, error) {
	if r == nil || r.pool == nil {
		return nil, ErrProxyResolverMisconfigured
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return nil, fmt.Errorf("provider proxy resolver: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// LEFT JOIN proxies; 返回 SQL 端字段 + 状态标志, Go 端组装 URL.
	// has_proxy_bound 区分 "无代理配置" vs "有代理但 unhealthy".
	// proxy_is_healthy 仅当 JOIN 命中 + status='active' + 未软删 时 true.
	const q = `
	        SELECT
	            pa.tenant_id                                                AS tenant_id,
	            COALESCE(pa.proxy_group_id, '')                            AS proxy_group_id,
	            TRUE AS account_exists,
	            pa.proxy_id IS NOT NULL                                    AS has_proxy_bound,
	            (p.id IS NOT NULL AND p.status = 'active' AND p.deleted_at IS NULL) AS proxy_is_healthy,
            COALESCE(p.protocol, '')      AS protocol,
            COALESCE(p.host, '')          AS host,
            COALESCE(p.port, 0)           AS port,
            p.auth_username                AS auth_username,
            p.auth_secret                  AS auth_secret,
            t.default_proxy_id IS NOT NULL AS has_default_bound,
            (dp.id IS NOT NULL AND dp.status = 'active' AND dp.deleted_at IS NULL) AS default_is_healthy,
            COALESCE(dp.protocol, '')     AS d_protocol,
            COALESCE(dp.host, '')         AS d_host,
            COALESCE(dp.port, 0)          AS d_port,
            dp.auth_username               AS d_auth_username,
            dp.auth_secret                 AS d_auth_secret,
            COALESCE(p.fallback_mode, 'reject')  AS fallback_mode,
            COALESCE(dp.fallback_mode, 'reject') AS d_fallback_mode
        FROM provider_accounts pa
        LEFT JOIN proxies p ON pa.proxy_id = p.id
        LEFT JOIN tenants t ON pa.tenant_id = t.id
        LEFT JOIN proxies dp ON t.default_proxy_id = dp.id
        WHERE pa.id = $1
    `
	var row proxyRow
	if err := tx.QueryRow(ctx, q, accountID).Scan(
		&row.tenantID,
		&row.proxyGroupID,
		&row.accountExists,
		&row.hasProxyBound,
		&row.proxyIsHealthy,
		&row.protocol,
		&row.host,
		&row.port,
		&row.username,
		&row.secret,
		&row.hasDefaultBound,
		&row.defaultIsHealthy,
		&row.dprotocol,
		&row.dhost,
		&row.dport,
		&row.dusername,
		&row.dsecret,
		&row.fallbackMode,
		&row.dFallbackMode,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("account %d: %w", accountID, ErrAccountNotFound)
		}
		return nil, fmt.Errorf("provider proxy resolver: query: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("provider proxy resolver: commit: %w", err)
	}

	// PROXY-05: 账号绑了代理组(且未单绑 proxy_id)-> 在组内 active 成员按账号
	// 确定性轮换。空组/无健康成员 fail-closed(不落直连)。优先级在单绑之后、
	// tenant-default 之前。
	if !row.hasProxyBound && row.proxyGroupID != "" {
		members, gerr := r.listGroupMembers(ctx, row.tenantID, row.proxyGroupID)
		if gerr != nil {
			return nil, fmt.Errorf("provider proxy resolver: list group members: %w", gerr)
		}
		chosen := pickGroupMember(accountID, members)
		if chosen == nil {
			return nil, fmt.Errorf("account %d: %w", accountID, ErrProxyUnhealthy)
		}
		grow := proxyRow{protocol: chosen.protocol, host: chosen.host, port: chosen.port, username: chosen.username, secret: chosen.secret}
		if err := r.decryptRowAuthSecret(ctx, &grow); err != nil {
			return nil, fmt.Errorf("provider proxy resolver: decrypt auth secret: %w", err)
		}
		return buildProxyURL(grow, accountID)
	}

	// 按优先级选层: account > tenant-default > direct。每层 bound-but-unhealthy
	// 都 fail-closed (不静默落到下层或直连, 否则破坏账号级 IP 隔离)。
	fields, useProxy, perr := chooseProxyTier(row, r.directFallbackAllowed != nil && r.directFallbackAllowed())
	if perr != nil {
		return nil, fmt.Errorf("account %d: %w", accountID, perr)
	}
	if !useProxy {
		return nil, nil
	}

	chosen := proxyRow{protocol: fields.protocol, host: fields.host, port: fields.port, username: fields.username, secret: fields.secret}
	if err := r.decryptRowAuthSecret(ctx, &chosen); err != nil {
		return nil, fmt.Errorf("provider proxy resolver: decrypt auth secret: %w", err)
	}
	return buildProxyURL(chosen, accountID)
}

func (r *PostgresProxyResolver) decryptRowAuthSecret(ctx context.Context, row *proxyRow) error {
	if row == nil || row.secret == nil || *row.secret == "" {
		return nil
	}
	plaintext, err := proxysecret.Decode(ctx, r.keys, row.tenantID, *row.secret)
	if err != nil {
		return err
	}
	row.secret = &plaintext
	return nil
}

// buildProxyURL 用 proxies 表字段构造 *url.URL。
// 关键: url.UserPassword() 自动 percent-encode user/secret, 避免 "/", "#",
// "?", "@" 等保留字符破坏 URL 解析. 跟 SQL 字符串拼接相比是 escape-safe.
//
// 单测路径: 输入 row 直接构造, 不走 DB.
func buildProxyURL(row proxyRow, accountID int64) (*url.URL, error) {
	if row.protocol == "" || row.host == "" || row.port <= 0 {
		return nil, fmt.Errorf("%w (account=%d): proxies 字段不完整 (protocol/host/port)", ErrProxyURLInvalid, accountID)
	}
	u := &url.URL{
		Scheme: row.protocol,
		Host:   net.JoinHostPort(row.host, strconv.Itoa(row.port)),
	}
	if row.username != nil && *row.username != "" {
		var secret string
		if row.secret != nil {
			secret = *row.secret
		}
		u.User = url.UserPassword(*row.username, secret)
	}
	// 二次校验 (defense in depth — 万一上面字段有非法字符, Parse 会报错)
	if _, err := url.Parse(u.String()); err != nil {
		return nil, fmt.Errorf("%w (account=%d): %v", ErrProxyURLInvalid, accountID, err)
	}
	return u, nil
}

// parseProxyURLValue 是 0038 之前的老入口, 保留供单测 (test_unit) 检查"输入
// 字符串 → *url.URL"路径. 0038 起 Resolve() 不再调用此函数, 但 parseProxyURLValue
// 单测仍覆盖 URL parsing 边界场景 (空串/缺 scheme/缺 host), 删除后失测试覆盖.
//
// 输入 raw 语义:
//   - nil / 空串 → (nil, nil)
//   - 合法 URL   → (*url.URL, nil)
//   - 非法 URL   → (nil, ErrProxyURLInvalid)
func parseProxyURLValue(raw *string, accountID int64) (*url.URL, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	parsed, err := url.Parse(*raw)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) && urlErr.Err != nil {
			return nil, fmt.Errorf("%w (account=%d): %v", ErrProxyURLInvalid, accountID, urlErr.Err)
		}
		return nil, fmt.Errorf("%w (account=%d): parse failed", ErrProxyURLInvalid, accountID)
	}
	if parsed.Scheme == "" {
		return nil, fmt.Errorf("%w (account=%d): scheme 缺失 (需 http/https/socks5 等显式 scheme)", ErrProxyURLInvalid, accountID)
	}
	if parsed.Hostname() == "" {
		return nil, fmt.Errorf("%w (account=%d, scheme=%q): host 为空", ErrProxyURLInvalid, accountID, parsed.Scheme)
	}
	return parsed, nil
}

// proxyFields 是选中那层代理的原始字段(secret 待解密)。
type proxyFields struct {
	protocol string
	host     string
	port     int
	username *string
	secret   *string
}

// fallback_mode 取值。
const (
	proxyFallbackReject = "reject"
	proxyFallbackDirect = "direct"
)

// chooseProxyTier 按优先级 account > tenant-default > direct 选出该用哪层代理。
// 默认每层 bound-but-unhealthy fail-closed(reject);仅当该层代理 fallback_mode='direct'
// 且平台总闸 directFallbackAllowed=true 时才允许落到直连(双重门,见 resolveUnhealthyTier)。
// useProxy=false + err=nil 表示直连。
func chooseProxyTier(row proxyRow, directFallbackAllowed bool) (proxyFields, bool, error) {
	if row.hasProxyBound {
		if !row.proxyIsHealthy {
			return resolveUnhealthyTier(row.fallbackMode, directFallbackAllowed)
		}
		return proxyFields{protocol: row.protocol, host: row.host, port: row.port, username: row.username, secret: row.secret}, true, nil
	}
	if row.hasDefaultBound {
		if !row.defaultIsHealthy {
			return resolveUnhealthyTier(row.dFallbackMode, directFallbackAllowed)
		}
		return proxyFields{protocol: row.dprotocol, host: row.dhost, port: row.dport, username: row.dusername, secret: row.dsecret}, true, nil
	}
	return proxyFields{}, false, nil
}

// resolveUnhealthyTier 决定 bound-but-unhealthy 代理层的行为。默认 'reject' = fail-closed
// (保护账号级 IP 隔离)。'direct' 仅当平台总闸 directFallbackAllowed 也为 true 时才落直连——
// 双重门:per-proxy 设置或平台总闸任一单独都无法把出站退到网关真实 IP。
func resolveUnhealthyTier(fallbackMode string, directFallbackAllowed bool) (proxyFields, bool, error) {
	if fallbackMode == proxyFallbackDirect && directFallbackAllowed {
		return proxyFields{}, false, nil
	}
	return proxyFields{}, false, ErrProxyUnhealthy
}

// listGroupMembers 列出某 tenant 某组的 active 代理成员(原始字段, secret 待解密)。
func (r *PostgresProxyResolver) listGroupMembers(ctx context.Context, tenantID int64, groupID string) ([]proxyFields, error) {
	const q = `
		SELECT COALESCE(protocol, ''), COALESCE(host, ''), COALESCE(port, 0), auth_username, auth_secret
		FROM proxies
		WHERE tenant_id = $1 AND group_id = $2 AND status = 'active' AND deleted_at IS NULL
		ORDER BY id`
	rows, err := r.pool.Query(ctx, q, tenantID, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []proxyFields
	for rows.Next() {
		var f proxyFields
		if err := rows.Scan(&f.protocol, &f.host, &f.port, &f.username, &f.secret); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// pickGroupMember 在组成员里按 accountID 确定性选一个(不同账号散布、同账号粘定)。
// 空列表返回 nil(调用方 fail-closed)。纯函数, 可单测。
func pickGroupMember(accountID int64, members []proxyFields) *proxyFields {
	if len(members) == 0 {
		return nil
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(strconv.FormatInt(accountID, 10)))
	idx := int(h.Sum32() % uint32(len(members)))
	return &members[idx]
}
