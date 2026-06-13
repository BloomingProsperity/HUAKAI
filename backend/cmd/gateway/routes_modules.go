package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/modulehttp"
)

// mountModuleRegistryRoutes wires the WAVE H2 read-only module-knowledge endpoint
// behind adminGate (platform-admin RBAC), mirroring routes_systemhealth.go.
//
// GET /admin/v1/modules            — merged identity + capabilities + status + live probe
// GET /admin/v1/modules?category=  — filter to one category
//
// Gating: reuses the SAME admin auth as every other /admin/v1/* route — the
// adminGate(adminIdentityResolver, handler) wrapper backed by d.adminAuth
// (*admin.AdminResolver), identical to how mountSystemHealthRoutes gates
// /admin/v1/system/health. No new auth is introduced.
func mountModuleRegistryRoutes(r chi.Router, d *deps) {
	if d == nil {
		return
	}
	var resolver adminIdentityResolver
	if d.adminAuth != nil {
		resolver = d.adminAuth
	}
	src := newModuleSource(d.moduleRegistry)
	h := modulehttp.NewModulesHandler(src)
	r.Method(http.MethodGet, "/admin/v1/modules", adminGate(resolver, h))
	r.Method(http.MethodGet, "/v1/admin/modules", adminGate(resolver, h))
}
