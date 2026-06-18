package quota

import (
	"context"
	"time"

	dbquota "github.com/BloomingProsperity/HUAKAI/internal/db/quota"
)

// Current-window read projections, grouped in a focused file. pg_store.go was 588 lines at
// HEAD; adding the new multi-metric read there would push it to 611 — past the 600-line
// per-file cap (CLAUDE.md #13) — so these read methods live here instead. The cost-only read
// serves subscription progress and key-control; the multi-metric read serves /quota status.

// ListCurrentWindowsForScope returns the COST-only current quota windows for a scope.
// It pins the metric filter to cost_usd so existing callers (subscription progress,
// key-control) keep their exact original behaviour — `= ANY({cost_usd})` is identical
// to the prior `= 'cost_usd'` predicate. Multi-metric self-service reads use
// ListCurrentWindowsForScopeMetrics instead.
func (s *PostgresStore) ListCurrentWindowsForScope(ctx context.Context, tenantID int64, scopeKind ScopeKind, scopeID string, at time.Time) ([]CurrentWindowRead, error) {
	return s.listCurrentWindows(ctx, tenantID, scopeKind, scopeID, at, []string{string(MetricCostUSD)})
}

// ListCurrentWindowsForScopeMetrics returns the current quota windows for a scope across
// the requested metrics (the self-service /quota read passes the window-shaped metrics:
// requests, cost_usd, tokens_estimated). Concurrency is intentionally excluded — it is a
// slot-based metric, not a window-accumulation counter, so it never produces a window row.
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
