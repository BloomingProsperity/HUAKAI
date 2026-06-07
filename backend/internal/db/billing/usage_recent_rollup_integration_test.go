//go:build integration_pg

package billing

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestRecentUsageRollupByTenant(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openUsageOutcomePool(t, ctx)
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin recent usage rollup tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tenantA := seedUsageOutcomeFixture(t, ctx, tx)
	_ = seedUsageOutcomeFixture(t, ctx, tx)
	base := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	seedUsageOutcomeRecord(t, ctx, tx, tenantA, "old-outside-window", "upstream_error_5xx", base.Add(-2*time.Hour))

	q := New(tx)
	// MUTATION: remove tenant_id or settled_at filter -> tenant B or old tenant A rows inflate this rollup -> RED.
	got, err := q.RecentUsageRollupByTenant(ctx, RecentUsageRollupByTenantParams{
		TenantID: tenantA.tenantID,
		SettledSince: pgtype.Timestamptz{
			Time:  base.Add(-time.Minute),
			Valid: true,
		},
	})
	if err != nil {
		t.Fatalf("RecentUsageRollupByTenant: %v", err)
	}
	if got.RequestCount != 3 {
		t.Fatalf("request_count=%d want 3", got.RequestCount)
	}
	if got.SuccessCount != 2 {
		t.Fatalf("success_count=%d want 2", got.SuccessCount)
	}
	if got.ErrorCount != 1 {
		t.Fatalf("error_count=%d want 1", got.ErrorCount)
	}
	if got.TotalCost != "0.03000000" {
		t.Fatalf("total_cost=%q want 0.03000000", got.TotalCost)
	}
}
