package alertmetrics

import (
	"context"
	"testing"
	"time"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// MUTATION: pass tenantID 0 or a stale settledSince to sqlc -> alert metrics are no longer tenant/window scoped -> RED.
func TestBillingRecentUsageRolluperCallsTenantScopedQuery(t *testing.T) {
	since := time.Date(2026, 6, 7, 12, 45, 0, 0, time.UTC)
	querier := &stubBillingRollupQuerier{
		row: dbbilling.RecentUsageRollupByTenantRow{
			RequestCount: 8,
			SuccessCount: 5,
			ErrorCount:   3,
			TotalCost:    "2.50000000",
			LatencyP95Ms: 910.5,
			LatencyP99Ms: 1750.75,
		},
	}
	rolluper := NewBillingRecentUsageRolluper(querier)

	got, err := rolluper.RecentUsageRollup(context.Background(), 1234, since)
	if err != nil {
		t.Fatalf("RecentUsageRollup() error = %v", err)
	}
	if len(querier.calls) != 1 {
		t.Fatalf("calls=%d want 1", len(querier.calls))
	}
	call := querier.calls[0]
	if call.TenantID != 1234 {
		t.Fatalf("TenantID=%d want 1234", call.TenantID)
	}
	if !call.SettledSince.Valid || !call.SettledSince.Time.Equal(since) {
		t.Fatalf("SettledSince=%+v want valid %s", call.SettledSince, since)
	}
	if got.RequestCount != 8 || got.SuccessCount != 5 || got.ErrorCount != 3 || got.TotalCostUSD != 2.5 {
		t.Fatalf("rollup=%+v want counts 8/5/3 cost 2.5", got)
	}
	// MUTATION: drop row.LatencyP95Ms/P99Ms from the mapping -> these go 0 -> RED.
	// p95 and p99 are distinct so a swapped mapping also fails.
	if got.LatencyP95MS != 910.5 || got.LatencyP99MS != 1750.75 {
		t.Fatalf("rollup latency=%v/%v want 910.5/1750.75", got.LatencyP95MS, got.LatencyP99MS)
	}
}

type stubBillingRollupQuerier struct {
	row   dbbilling.RecentUsageRollupByTenantRow
	err   error
	calls []dbbilling.RecentUsageRollupByTenantParams
}

func (s *stubBillingRollupQuerier) RecentUsageRollupByTenant(_ context.Context, arg dbbilling.RecentUsageRollupByTenantParams) (dbbilling.RecentUsageRollupByTenantRow, error) {
	s.calls = append(s.calls, arg)
	if s.err != nil {
		return dbbilling.RecentUsageRollupByTenantRow{}, s.err
	}
	return s.row, nil
}
