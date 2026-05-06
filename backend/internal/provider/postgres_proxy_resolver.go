// 包 provider — ProxyResolver 的 PostgreSQL 后端实现。
// 本文件实现 PostgresProxyResolver，从 provider_accounts.proxy_url 列读取
// 账号级出站代理 URL，与 PostgresCredentialVault 共用 provider_accounts 表。
//
// 语义（与 ProxyResolver 接口对齐）：
//   - 行存在 + proxy_url 非空    → 解析 URL，走该代理
//   - 行存在 + proxy_url IS NULL → 直连（返回 nil URL + nil err）
//   - 行不存在                   → ErrAccountNotFound
//   - URL 格式错误               → 包装 net/url.Parse 错误
//   - DB 故障                    → 包装底层 pgx 错误
package provider

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrProxyURLInvalid 表示 provider_accounts.proxy_url 列值无法被
// net/url.Parse 解析。包装底层 url.Parse 错误，可用 errors.Unwrap 取原因。
var ErrProxyURLInvalid = errors.New("provider proxy resolver: proxy_url 格式无效")

// PostgresProxyResolver 是基于 PostgreSQL 的 ProxyResolver 实现。
// 与 PostgresCredentialVault 共享 provider_accounts 表，只读取 proxy_url 列。
//
// 使用 REPEATABLE READ + READ ONLY 事务，保持与其它 Postgres 仓库读取一致。
type PostgresProxyResolver struct {
	pool *pgxpool.Pool
}

// 编译期接口合规断言。
var _ ProxyResolver = (*PostgresProxyResolver)(nil)

// NewPostgresProxyResolver 用给定的连接池创建 PostgresProxyResolver。
// pool 不应为 nil；调用方负责池的生命周期管理。
func NewPostgresProxyResolver(pool *pgxpool.Pool) *PostgresProxyResolver {
	return &PostgresProxyResolver{pool: pool}
}

// Resolve 按 accountID 查询 provider_accounts.proxy_url。
//
// 返回值语义：
//   - (*url.URL, nil)            ：proxy_url 非空且解析成功
//   - (nil, nil)                 ：行存在但 proxy_url 为 NULL（明确直连）
//   - (nil, ErrAccountNotFound)  ：account 不存在
//   - (nil, ErrProxyURLInvalid)  ：proxy_url 列存在但 URL 解析失败
//   - (nil, 其它 error)          ：DB 基础设施故障
func (r *PostgresProxyResolver) Resolve(ctx context.Context, accountID int64) (*url.URL, error) {
	// nil receiver / nil pool 是 DI / 配置错误，**必须** fail-loud。
	// 不能返回 ErrAccountNotFound（dispatcher 会当成"直连"放行，造成所有账号
	// 静默绕过代理 → 破坏账号级 IP 隔离 = 安全漏洞）。
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

	const q = `SELECT proxy_url FROM provider_accounts WHERE id = $1`
	var raw *string
	if err := tx.QueryRow(ctx, q, accountID).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("account %d: %w", accountID, ErrAccountNotFound)
		}
		return nil, fmt.Errorf("provider proxy resolver: query: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("provider proxy resolver: commit: %w", err)
	}

	return parseProxyURLValue(raw, accountID)
}

// parseProxyURLValue 把 DB 列里的可空字符串映射为 *url.URL。
// 抽到独立函数以便单测覆盖（DB 路径见 integration_pg 测试）。
//
// 输入 raw 语义：
//   - nil / 空串 → 返回 (nil, nil)（明确直连）
//   - 合法 URL   → 返回 (*url.URL, nil)
//   - 非法 URL   → 返回 (nil, ErrProxyURLInvalid)
//
// 安全：错误消息不包含 raw URL（socks5://user:pass@... 会泄漏代理账号
// 密码到日志/error chain）。失败时只输出 accountID + 原因摘要，密码
// 字段永远不进 error。
func parseProxyURLValue(raw *string, accountID int64) (*url.URL, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	parsed, err := url.Parse(*raw)
	if err != nil {
		// 不暴露 raw — url.Error.URL 字段会带原 URL（含 user:pass）。
		// 用 errors.As 取出内层 Err（不含 URL），保持错误链可调试同时
		// 不泄漏代理凭据。
		var urlErr *url.Error
		if errors.As(err, &urlErr) && urlErr.Err != nil {
			return nil, fmt.Errorf("%w (account=%d): %v", ErrProxyURLInvalid, accountID, urlErr.Err)
		}
		return nil, fmt.Errorf("%w (account=%d): parse failed", ErrProxyURLInvalid, accountID)
	}
	// url.Parse 对 "not_a_url" 不报错却得到没有 scheme 的 URL → 显式守住
	if parsed.Scheme == "" {
		return nil, fmt.Errorf("%w (account=%d): scheme 缺失（需 http/https/socks5 等显式 scheme）", ErrProxyURLInvalid, accountID)
	}
	// scheme 存在但 host 缺失（如 "http://" / "http:foo"）→ 显式拒绝
	if parsed.Hostname() == "" {
		return nil, fmt.Errorf("%w (account=%d, scheme=%q): host 为空", ErrProxyURLInvalid, accountID, parsed.Scheme)
	}
	return parsed, nil
}
