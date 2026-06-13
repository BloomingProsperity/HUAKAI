package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/modulehttp"
	"github.com/BloomingProsperity/HUAKAI/internal/moduleregistry"
)

// TestModulesEndpointIsAdminGated — a no-credential request to /admin/v1/modules
// is handled by the SAME admin middleware as every other /admin/v1 route.
//
// Regression (mutation: drop adminGate from routes_modules.go and mount the bare
// handler): with adminGate present and a nil-queries admin resolver, the request
// fails closed as 503 admin_backend_error — a code only the admin gate emits. If
// the gate were removed, the bare handler would return 200 with a modules body
// instead, and BOTH the status and the missing-code assertions go RED. This is
// the discriminating fixture: gated => 503/admin_backend_error; ungated => 200.
func TestModulesEndpointIsAdminGated(t *testing.T) {
	d := &deps{
		// nil queries -> Resolve returns ErrAdminBackend (fail-closed 503),
		// exactly as the Hermes admin-gate test relies on.
		adminAuth:      admin.NewAdminResolver(nil),
		moduleRegistry: moduleregistry.New(),
	}
	r := chi.NewRouter()
	// Mount ONLY the module-registry routes (the real adminGate wrapper is
	// exercised here); mountAdminRoutes as a whole nil-derefs on a minimal deps
	// because it wires dozens of DB-backed subsystems unrelated to this test.
	mountModuleRegistryRoutes(r, d)

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/modules", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503 (admin gate, not bare handler)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "admin_backend_error") {
		t.Fatalf("body=%s want admin_backend_error (proves admin gate mounted, not the 200 module list)", rec.Body.String())
	}
}

// TestModulesEndpointReturnsSeededModulesForAdmin — with the gate satisfied (an
// injected platform-admin identity), the merged handler returns the live seeds.
// This exercises the handler+source wiring with a REAL registry seeded by
// buildModuleRegistry, not a fake, so a regression in the seed registrations
// (e.g. a missing Register call) goes RED here.
//
// Regression: if buildModuleRegistry stopped registering the three seeds, the
// count assertion (>=3) goes RED; if the handler dropped the body, decode fails.
func TestModulesEndpointReturnsSeededModulesForAdmin(t *testing.T) {
	// Build a real seeded registry from a minimal deps (probe-referenced fields
	// left nil -> probes report degraded, which is fine: we assert identity, not
	// health, here).
	d := &deps{}
	reg := buildModuleRegistry(d)
	src := newModuleSource(reg)

	// Call the handler directly (gate is asserted separately above); this keeps
	// the positive test free of DB-backed admin resolution.
	h := modulehttp.NewModulesHandler(src)
	req := httptest.NewRequest(http.MethodGet, "/admin/v1/modules", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp modulehttp.ModulesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Modules) < 3 {
		t.Fatalf("modules=%d want >=3 seeded (billing/routing/credentials)", len(resp.Modules))
	}
	// The billing seed must carry its static catalog overlay (join wired).
	var sawBillingOverlay bool
	for _, m := range resp.Modules {
		if m.ID == "billing.service" && m.Catalog != nil && m.Catalog.FeatureID != "" {
			sawBillingOverlay = true
		}
	}
	if !sawBillingOverlay {
		t.Fatalf("billing.service missing static catalog overlay; seedCatalogJoin/merge regression")
	}
}

// TestModulesEndpointCategoryFilterForAdmin — ?category= narrows the live seeds.
// Regression: if the handler ignored ?category=, money-path would return all 3
// seeds instead of just billing.service -> RED. Discriminating because the seeds
// span three DISTINCT categories.
func TestModulesEndpointCategoryFilterForAdmin(t *testing.T) {
	src := newModuleSource(buildModuleRegistry(&deps{}))
	h := modulehttp.NewModulesHandler(src)

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/modules?category=money-path", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	var resp modulehttp.ModulesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Modules) != 1 || resp.Modules[0].ID != "billing.service" {
		ids := make([]string, len(resp.Modules))
		for i, m := range resp.Modules {
			ids[i] = m.ID
		}
		t.Fatalf("money-path filter -> %v want [billing.service]", ids)
	}
}
