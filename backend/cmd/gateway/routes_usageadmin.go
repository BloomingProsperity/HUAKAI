package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/usageanalyticshttp"
)

// mountUsageAdminRoutes 把 platform-admin 的用量成本排行榜挂载到
// adminGate（platform-admin RBAC）之后。只读；它有意上报 actual_cost
// 供运维做支出分析。typed-nil resolver 的坍缩处理沿用 /debug/vars 的做法，
// 这样在 deps 未配置时仍会返回 admin_gate_not_configured(503) 而非 panic。
func mountUsageAdminRoutes(r chi.Router, d *deps) {
	if d == nil {
		return
	}
	var resolver adminIdentityResolver
	if d.adminAuth != nil {
		resolver = d.adminAuth
	}
	r.Method(http.MethodGet, "/v1/admin/usage/leaderboard",
		adminGate(resolver, usageanalyticshttp.NewLeaderboardHandler(d.billingQueries)))
	r.Method(http.MethodGet, "/v1/admin/usage/performance",
		adminGate(resolver, usageanalyticshttp.NewPerformanceHandler(d.billingQueries)))
	r.Method(http.MethodGet, "/v1/admin/usage/perf-metrics/summary",
		adminGate(resolver, usageanalyticshttp.NewPerfMetricsSummaryHandler(d.billingQueries)))
	r.Method(http.MethodGet, "/v1/admin/usage/perf-metrics/by-bucket",
		adminGate(resolver, usageanalyticshttp.NewPerfMetricsByBucketHandler(d.billingQueries)))
	r.Method(http.MethodGet, "/v1/admin/usage/health-score",
		adminGate(resolver, usageanalyticshttp.NewHealthScoreHandler(d.billingQueries, d.channelHealth)))
	r.Method(http.MethodGet, "/v1/admin/usage/overview",
		adminGate(resolver, usageanalyticshttp.NewOverviewHandler(d.billingQueries)))
	r.Method(http.MethodGet, "/v1/admin/usage/provider-account-counts",
		adminGate(resolver, usageanalyticshttp.NewProviderAccountCountsHandler(d.billingQueries)))
}
