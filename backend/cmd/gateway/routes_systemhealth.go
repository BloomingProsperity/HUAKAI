package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/servermonitor"
	"github.com/BloomingProsperity/HUAKAI/internal/servermonitorhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/systemhealthhttp"
)

// mountSystemHealthRoutes 接线 ADMIN-042 只读的系统健康聚合，
// 置于 adminGate（platform-admin RBAC）之后。沿用 routes_usageadmin.go 的模式。
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
	monitor := servermonitorhttp.New(d.serverMonitorStore, d.serverMonitorOffline)
	for _, prefix := range []string{"/v1/admin/system/nodes", "/admin/v1/system/nodes"} {
		r.Method(http.MethodGet, prefix, adminGate(resolver, http.HandlerFunc(monitor.List)))
		r.Method(http.MethodGet, prefix+"/{node_id}", adminGate(resolver, http.HandlerFunc(monitor.Detail)))
		r.Method(http.MethodGet, prefix+"/{node_id}/history", adminGate(resolver, http.HandlerFunc(monitor.History)))
	}
}

// gatewaySystemHealthSource 把运行期 deps 字段适配到 SystemHealthSource。
// 所有读取都是只读快照；零计费副作用。
type gatewaySystemHealthSource struct {
	db             systemHealthDB
	monitorStore   *servermonitor.PostgresStore
	monitorEnabled bool
	monitorOffline time.Duration
}

type systemHealthDB interface {
	Ping(context.Context) error
	QueryRow(context.Context, string, ...any) pgx.Row
}

func buildSystemHealthSource(d *deps) systemhealthhttp.SystemHealthSource {
	if d == nil {
		return nil
	}
	var db systemHealthDB
	if d.pgPool != nil {
		db = d.pgPool
	}
	return &gatewaySystemHealthSource{
		db:             db,
		monitorStore:   d.serverMonitorStore,
		monitorEnabled: d.serverMonitorEnabled,
		monitorOffline: d.serverMonitorOffline,
	}
}

func (s *gatewaySystemHealthSource) ServerMonitorSummary(ctx context.Context) (bool, int64, int64, int64, error) {
	if !s.monitorEnabled {
		return false, 0, 0, 0, nil
	}
	if s.monitorStore == nil {
		return true, 0, 0, 0, errors.New("server monitor store is unavailable")
	}
	summary, err := s.monitorStore.Summary(ctx, time.Now().UTC(), s.monitorOffline)
	if err != nil {
		return true, 0, 0, 0, err
	}
	return true, summary.Total, summary.Offline, summary.Degraded, nil
}

func (s *gatewaySystemHealthSource) DBPing(ctx context.Context) error {
	if s.db == nil {
		return errors.New("system health database is unavailable")
	}
	return s.db.Ping(ctx)
}

func (s *gatewaySystemHealthSource) ChannelHealthSummary(ctx context.Context) (total int64, unhealthy int64, err error) {
	if s.db == nil {
		return 0, 0, errors.New("system health database is unavailable")
	}
	err = s.db.QueryRow(ctx, `
SELECT COUNT(*)::bigint,
       COUNT(*) FILTER (WHERE state IN ('cooling_down', 'disabled', 'degraded'))::bigint
FROM channel_health_state`).Scan(&total, &unhealthy)
	return total, unhealthy, err
}

func (s *gatewaySystemHealthSource) DLQPendingDepth(ctx context.Context) (int64, error) {
	if s.db == nil {
		return 0, errors.New("system health database is unavailable")
	}
	var depth int64
	err := s.db.QueryRow(ctx, `
SELECT COUNT(*)::bigint
FROM usage_record_dlq
WHERE status = 'pending'`).Scan(&depth)
	return depth, err
}

func (s *gatewaySystemHealthSource) AlertingFiringCount(ctx context.Context) (int64, error) {
	if s.db == nil {
		return 0, errors.New("system health database is unavailable")
	}
	var count int64
	err := s.db.QueryRow(ctx, `
SELECT COUNT(*)::bigint
FROM alert_events
WHERE state = 'firing'`).Scan(&count)
	return count, err
}
