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
}

// gatewaySystemHealthSource 把运行期 deps 字段适配到 SystemHealthSource。
// 所有读取都是只读快照；零计费副作用。
type gatewaySystemHealthSource struct {
	pool       *pgxpool.Pool
	chService  *channelhealth.Service
	dlqService *legacydlq.Service
	alertSvc   *alerting.Service
	tenantIDFn func() int64 // 对 platform-admin 返回 0（无需跨租户查询：用 0 表示全局）
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
		return nil // 未配置连接池 —— 视为健康（standalone 单机模式）
	}
	return s.pool.Ping(ctx)
}

func (s *gatewaySystemHealthSource) ChannelHealthSummary(ctx context.Context) (total int64, unhealthy int64, err error) {
	if s.chService == nil {
		return 0, 0, nil
	}
	// SummarizeChannelHealth 使用 tenantID=0 表示跨租户的平台视图。
	// 实际上调用方应限定到平台级的哨兵租户；0 是现有 gatewayhttp 渠道健康
	// 管理路由所采用的约定。
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
		Limit:  10000, // 上限；不要求精确深度，只需非零信号即可
	})
	if err != nil {
		return 0, err
	}
	return int64(len(records)), nil
}

func (s *gatewaySystemHealthSource) AlertingFiringCount(ctx context.Context) (int64, error) {
	// alertSvc 是可选的 —— 并非所有部署都配置告警（alerting）。
	// 未设置时返回 0（视为健康）。
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
