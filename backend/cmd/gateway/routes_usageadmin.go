package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/usageanalyticshttp"
)

// mountUsageAdminRoutes mounts the platform-admin usage cost leaderboard behind
// adminGate (platform-admin RBAC). Read-only; it intentionally reports
// actual_cost for operator spend analysis. The typed-nil resolver collapse
// mirrors /debug/vars so an unconfigured deps still yields
// admin_gate_not_configured(503) rather than panicking.
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
}
