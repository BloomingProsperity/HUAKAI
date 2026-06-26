package quota

import (
	"context"
	"time"

	dbquota "github.com/BloomingProsperity/HUAKAI/internal/db/quota"
)

// 当前窗口的只读投影, 集中放在这个专门文件里。pg_store.go 在 HEAD 处已有 588 行;
// 若把新的多 metric 读取也加进去会涨到 611 行, 超过每文件 600 行上限(CLAUDE.md #13),
// 所以这些读取方法改放在这里。cost-only 读取服务于 subscription 进度与 key-control;
// 多 metric 读取服务于 /quota 状态。

// ListCurrentWindowsForScope 返回某 scope 仅 COST 维度的当前 quota 窗口。
// 它把 metric 过滤固定为 cost_usd, 让既有调用方(subscription 进度、
// key-control)保持完全一致的原始行为——`= ANY({cost_usd})` 与之前的
// `= 'cost_usd'` 谓词等价。多 metric 自助读取改用
// ListCurrentWindowsForScopeMetrics。
func (s *PostgresStore) ListCurrentWindowsForScope(ctx context.Context, tenantID int64, scopeKind ScopeKind, scopeID string, at time.Time) ([]CurrentWindowRead, error) {
	return s.listCurrentWindows(ctx, tenantID, scopeKind, scopeID, at, []string{string(MetricCostUSD)})
}

// ListCurrentWindowsForScopeMetrics 返回某 scope 在所请求 metric 集合上的当前 quota 窗口
// (自助 /quota 读取传入的是窗口型 metric: requests、cost_usd、tokens_estimated)。
// concurrency 被有意排除——它是基于 slot 的 metric, 不是窗口累加计数, 因此永远不会产生窗口行。
func (s *PostgresStore) ListCurrentWindowsForScopeMetrics(ctx context.Context, tenantID int64, scopeKind ScopeKind, scopeID string, at time.Time, metrics []Metric) ([]CurrentWindowRead, error) {
	metricStrs := make([]string, len(metrics))
	for i, m := range metrics {
		metricStrs[i] = string(m)
	}
	return s.listCurrentWindows(ctx, tenantID, scopeKind, scopeID, at, metricStrs)
}

func (s *PostgresStore) listCurrentWindows(ctx context.Context, tenantID int64, scopeKind ScopeKind, scopeID string, at time.Time, metrics []string) ([]CurrentWindowRead, error) {
	q, err := s.queries()
	if err != nil {
		return nil, err
	}
	rows, err := q.ListCurrentQuotaWindowsForScope(ctx, dbquota.ListCurrentQuotaWindowsForScopeParams{
		TenantID:  tenantID,
		ScopeKind: string(scopeKind),
		ScopeID:   normalizeScopeID(scopeKind, scopeID),
		AtTime:    pgTimestamptz(at.UTC()),
		Metrics:   metrics,
	})
	if err != nil {
		return nil, err
	}
	windows := make([]CurrentWindowRead, 0, len(rows))
	for _, row := range rows {
		windows = append(windows, currentWindowReadFromDB(row, at.UTC()))
	}
	return windows, nil
}
