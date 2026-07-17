// DM-14:account-health 告警指标——让"被自动摘除的账号"对告警引擎可见。
package alertmetrics

import (
	"context"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

const (
	// MetricAccountUnhealthyCount 当前被自动摘除(非 healthy 且仍在生效期)
	// 的账号总数;按状态细分见 account.unhealthy_<state>
	// (如 account.unhealthy_cooldown)。
	MetricAccountUnhealthyCount = "account.unhealthy_count"
	// MetricAccountUnhealthyPrefix 是按健康状态输出账号数的指标名前缀。
	MetricAccountUnhealthyPrefix = "account.unhealthy_"
)

// AccountHealthCounter 供 CompositeMetricSource 拉取 per-tenant 非健康账号
// 计数(state → count)。nil 表示未接线(指标族整体缺席)。
type AccountHealthCounter interface {
	UnhealthyAccountCounts(ctx context.Context, tenantID int64) (map[string]int64, error)
}

// PoolAccountHealthQuerier 是 sqlc 生成层的最小子集。
type PoolAccountHealthQuerier interface {
	CountUnhealthyAccountsByTenant(context.Context, int64) ([]dbbilling.CountUnhealthyAccountsByTenantRow, error)
}

type poolAccountHealthCounter struct{ q PoolAccountHealthQuerier }

// NewPoolAccountHealthCounter 用 dbbilling 查询实现 AccountHealthCounter。
func NewPoolAccountHealthCounter(q PoolAccountHealthQuerier) AccountHealthCounter {
	if q == nil {
		return nil
	}
	return poolAccountHealthCounter{q: q}
}

func (c poolAccountHealthCounter) UnhealthyAccountCounts(ctx context.Context, tenantID int64) (map[string]int64, error) {
	rows, err := c.q.CountUnhealthyAccountsByTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.HealthState] = row.AccountCount
	}
	return out, nil
}
