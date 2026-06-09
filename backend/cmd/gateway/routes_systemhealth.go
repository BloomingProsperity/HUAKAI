package main

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/alerting"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	legacydlq "github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/systemhealthhttp"
)

// mountSystemHealthRoutes wires ADMIN-042 read-only system health aggregation
// behind adminGate (platform-admin RBAC). Mirrors routes_usageadmin.go pattern.
func mountSystemHealthRoutes(r chi.Router, d *deps) {
	if d == nil {
		return
	}
	var resolver adminIdentityResolver
	if d.adminAuth != nil {
		resolver = d.adminAuth
	}
	src := buildSystemHealthSource(d)
	h := systemhealthhttp.NewSystemHealthHandler(src)
	r.Method(http.MethodGet, "/v1/admin/system/health", adminGate(resolver, h))
	r.Method(http.MethodGet, "/admin/v1/system/health", adminGate(resolver, h))
}

// gatewaySystemHealthSource adapts live deps fields to SystemHealthSource.
// All reads are read-only snapshots; zero billing side effects.
type gatewaySystemHealthSource struct {
	pool       *pgxpool.Pool
	chService  *channelhealth.Service
	dlqService *legacydlq.Service
	alertSvc   *alerting.Service
	tenantIDFn func() int64 // returns 0 for platform-admin (cross-tenant query not needed: uses 0 for global)
}

func buildSystemHealthSource(d *deps) systemhealthhttp.SystemHealthSource {
	if d == nil {
		return nil
	}
	return &gatewaySystemHealthSource{
		pool:       d.pgPool,
		chService:  d.channelHealth,
		dlqService: d.dlqService,
	}
}

func (s *gatewaySystemHealthSource) DBPing(ctx context.Context) error {
	if s.pool == nil {
		return nil // no pool configured — treated as healthy (standalone mode)
	}
	return s.pool.Ping(ctx)
}

func (s *gatewaySystemHealthSource) ChannelHealthSummary(ctx context.Context) (total int64, unhealthy int64, err error) {
	if s.chService == nil {
		return 0, 0, nil
	}
	// SummarizeChannelHealth uses tenantID=0 for cross-tenant platform view.
	// In practice callers should scope to a platform-wide sentinel tenant; 0 is
	// the convention used by existing gatewayhttp channel health admin routes.
	summary, err := s.chService.SummarizeChannelHealth(ctx, 0)
	if err != nil {
		return 0, 0, err
	}
	total = summary.Total
	// unhealthy = cooling_down + disabled + degraded
	unhealthy = summary.ByState[channelhealth.StateCoolingDown] +
		summary.ByState[channelhealth.StateDisabled] +
		summary.ByState[channelhealth.StateDegraded]
	return total, unhealthy, nil
}

func (s *gatewaySystemHealthSource) DLQPendingDepth(ctx context.Context) (int64, error) {
	if s.dlqService == nil {
		return 0, nil
	}
	records, err := s.dlqService.List(ctx, legacydlq.ListFilter{
		Status: legacydlq.StatusPending,
		Limit:  10000, // cap; exact depth not required, just non-zero signal
	})
	if err != nil {
		return 0, err
	}
	return int64(len(records)), nil
}

func (s *gatewaySystemHealthSource) AlertingFiringCount(ctx context.Context) (int64, error) {
	// alertSvc is optional — not all deploys configure alerting.
	// Return 0 (healthy) if unset.
	if s.alertSvc == nil {
		return 0, nil
	}
	events, err := s.alertSvc.ListEvents(ctx, alerting.ListEventsInput{
		TenantID: 0,
		State:    alerting.EventStateFiring,
		Limit:    10000,
	})
	if err != nil {
		return 0, err
	}
	return int64(len(events)), nil
}
