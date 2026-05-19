// 包 provider — ProxyResolver 的 PostgreSQL 后端实现。
// F-FP-POOL Phase 1.2 (2026-05-19): 代理资源迁出 provider_accounts.proxy_url
// 单字符串列, 进 proxies 独立表 (tenant 范围化); provider_accounts 通过
// proxy_id FK 关联。本文件 JOIN proxies 表取出字段在 Go 端用 url.URL{} +
// url.UserPassword() 构造代理 URL, 避免 SQL 字符串拼接破坏 URL 转义。
//
// 失效语义 (fail-closed, 不绕过代理):
//   - account.proxy_id IS NULL                                   → 直连 (admin 显式选无代理)
//   - account.proxy_id 指向 proxies 且 status='active' + 未软删   → 该代理 URL
//   - account.proxy_id 指向 proxies 但 status != 'active' / 软删 → ErrProxyUnhealthy
//                                                                  (调用方应拒绝出站, 强制
//                                                                  admin 介入. 不静默走直连,
//                                                                  避免破坏账号级 IP 隔离)
//   - account 行不存在                                            → ErrAccountNotFound
//   - URL 重建后格式错误                                          → ErrProxyURLInvalid
//   - DB 故障                                                    → 包装底层 pgx 错误
package provider

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"

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
}

// 编译期接口合规断言。
var _ ProxyResolver = (*PostgresProxyResolver)(nil)

// NewPostgresProxyResolver 用给定的连接池创建 PostgresProxyResolver。
func NewPostgresProxyResolver(pool *pgxpool.Pool) *PostgresProxyResolver {
	return &PostgresProxyResolver{pool: pool}
}

// proxyRow 是 SQL JOIN 返回的中间值, 跟 Go *url.URL 构造解耦.
type proxyRow struct {
	accountExists  bool
	hasProxyBound  bool   // account.proxy_id IS NOT NULL
	proxyIsHealthy bool   // 绑定的代理同时满足 status='active' + 未软删
	protocol       string // 仅 healthy=true 时有效
	host           string
	port           int
	username       *string
	secret         *string
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
            TRUE AS account_exists,
            pa.proxy_id IS NOT NULL                                    AS has_proxy_bound,
            (p.id IS NOT NULL AND p.status = 'active' AND p.deleted_at IS NULL) AS proxy_is_healthy,
            COALESCE(p.protocol, '')      AS protocol,
            COALESCE(p.host, '')          AS host,
            COALESCE(p.port, 0)           AS port,
            p.auth_username                AS auth_username,
            p.auth_secret                  AS auth_secret
        FROM provider_accounts pa
        LEFT JOIN proxies p ON pa.proxy_id = p.id
        WHERE pa.id = $1
    `
	var row proxyRow
	if err := tx.QueryRow(ctx, q, accountID).Scan(
		&row.accountExists,
		&row.hasProxyBound,
		&row.proxyIsHealthy,
		&row.protocol,
		&row.host,
		&row.port,
		&row.username,
		&row.secret,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("account %d: %w", accountID, ErrAccountNotFound)
		}
		return nil, fmt.Errorf("provider proxy resolver: query: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("provider proxy resolver: commit: %w", err)
	}

	// 1. 没绑代理 → 直连 (跟老 proxy_url IS NULL 行为一致)
	if !row.hasProxyBound {
		return nil, nil
	}

	// 2. 绑了但不健康 → fail-closed, 不静默走直连
	if !row.proxyIsHealthy {
		return nil, fmt.Errorf("account %d: %w", accountID, ErrProxyUnhealthy)
	}

	// 3. 健康 → 构造 URL (用 url.UserPassword 转义 user info, net.JoinHostPort 处理 host/port)
	return buildProxyURL(row, accountID)
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
