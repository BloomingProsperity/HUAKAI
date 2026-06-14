package alertmetrics

import (
	"context"
	"fmt"
	"strconv"
	"time"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/jackc/pgx/v5/pgtype"
)

type BillingRecentUsageQuerier interface {
	RecentUsageRollupByTenant(context.Context, dbbilling.RecentUsageRollupByTenantParams) (dbbilling.RecentUsageRollupByTenantRow, error)
}

type billingRecentUsageRolluper struct {
	querier BillingRecentUsageQuerier
}

func NewBillingRecentUsageRolluper(querier BillingRecentUsageQuerier) RecentUsageRolluper {
	if querier == nil {
		return nil
	}
	return billingRecentUsageRolluper{querier: querier}
}

func (r billingRecentUsageRolluper) RecentUsageRollup(ctx context.Context, tenantID int64, settledSince time.Time) (RecentUsageRollup, error) {
	if r.querier == nil {
		return RecentUsageRollup{}, fmt.Errorf("alertmetrics: usage rollup querier not configured")
	}
	row, err := r.querier.RecentUsageRollupByTenant(ctx, dbbilling.RecentUsageRollupByTenantParams{
		TenantID: tenantID,
		SettledSince: pgtype.Timestamptz{
			Time:  settledSince,
			Valid: true,
		},
	})
	if err != nil {
		return RecentUsageRollup{}, err
	}
	totalCost, err := strconv.ParseFloat(row.TotalCost, 64)
	if err != nil {
		return RecentUsageRollup{}, fmt.Errorf("alertmetrics: parse recent usage total_cost: %w", err)
	}
	return RecentUsageRollup{
		RequestCount: row.RequestCount,
		SuccessCount: row.SuccessCount,
		ErrorCount:   row.ErrorCount,
		TotalCostUSD: totalCost,
		LatencyP95MS: row.LatencyP95Ms,
		LatencyP99MS: row.LatencyP99Ms,
	}, nil
}
