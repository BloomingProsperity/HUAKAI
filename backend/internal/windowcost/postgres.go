package windowcost

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresLister 基于 pgx pool 实现 Lister。
type PostgresLister struct {
	pool *pgxpool.Pool
}

// NewPostgresLister 构造一个 PostgresLister。
func NewPostgresLister(pool *pgxpool.Pool) *PostgresLister {
	return &PostgresLister{pool: pool}
}

// ListLimitedAccounts 返回 window_cost_limit_cents > 0 且**窗口仍活动**(session_window_5h_start
// 非空且 session_window_5h_end > now())的账号。
//
// **必须过滤 session_window_5h_end > now()**(对抗 bug-hunt S2):session_window_5h_start/end 只在收到
// 上游新的 5h 限流头时被覆盖更新,窗口自然走完后这两列从不被清空/归零。若不加上界过滤,一个 5h 窗口
// 已结束、本应进入全新空窗口(零花费)的健康账号,会被 worker 以陈旧 windowStart 聚合到上一个窗口
// 整 5 小时的历史花费、判超限并持续下线(自增强卡死:被下线→收不到新流量→拿不到新窗口头→一直卡)。
// 加上界后,窗口结束即不再列出→worker 停止刷新其缓存→staleDuration(3min)后条目陈旧→gate fail-open
// 放行(账号自愈),恢复本包"绝不错误下线健康账号"的安全不变量。
func (l *PostgresLister) ListLimitedAccounts(ctx context.Context) ([]AccountRecord, error) {
	const q = `
SELECT pa.id, pa.tenant_id, pa.session_window_5h_start
FROM provider_accounts pa
JOIN tenants t
  ON t.id = pa.tenant_id
 AND t.status = 'active'
 AND t.deleted_at IS NULL
WHERE pa.window_cost_limit_cents > 0
  AND pa.session_window_5h_start IS NOT NULL
  AND pa.session_window_5h_end > now()
  AND pa.deleted_at IS NULL
`
	rows, err := l.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("windowcost: query limited accounts: %w", err)
	}
	defer rows.Close()

	var out []AccountRecord
	for rows.Next() {
		var r AccountRecord
		var windowStart time.Time
		if err := rows.Scan(&r.ID, &r.TenantID, &windowStart); err != nil {
			return nil, fmt.Errorf("windowcost: scan limited account: %w", err)
		}
		r.SessionWindow5hStart = windowStart
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("windowcost: iterate limited accounts: %w", err)
	}
	return out, nil
}

// PostgresAggregator 基于 pgx pool 实现 Aggregator。
type PostgresAggregator struct {
	pool *pgxpool.Pool
}

// NewPostgresAggregator 构造一个 PostgresAggregator。
func NewPostgresAggregator(pool *pgxpool.Pool) *PostgresAggregator {
	return &PostgresAggregator{pool: pool}
}

// SumWindowCost 汇总该账号自 windowStart 起的 usage_records.actual_cost。
// actual_cost 是以 USD 表示的 numeric(20,8);为了在 gate 中做整数比较,
// 我们将其换算为分(乘以 100,截断取整)。
func (a *PostgresAggregator) SumWindowCost(ctx context.Context, accountID int64, windowStart time.Time) (int64, error) {
	const q = `
SELECT COALESCE(SUM(actual_cost), 0)::numeric(20,8)::text
FROM usage_records
WHERE provider_account_id = $1
  AND settled_at >= $2
`
	var raw string
	if err := a.pool.QueryRow(ctx, q, accountID, windowStart).Scan(&raw); err != nil {
		return 0, fmt.Errorf("windowcost: sum window cost account=%d: %w", accountID, err)
	}
	// 解析 numeric 文本并换算为分(截断,而非四舍五入)。
	f, ok := new(big.Float).SetString(raw)
	if !ok {
		return 0, fmt.Errorf("windowcost: parse cost %q for account=%d", raw, accountID)
	}
	// 乘以 100 得到分。
	cents, _ := new(big.Float).Mul(f, big.NewFloat(100)).Int64()
	return cents, nil
}
