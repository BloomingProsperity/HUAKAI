package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelsRouteMounted(t *testing.T) {
	r := buildTestRouter(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)

	r.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("GET /v1/models returned 404; route must be mounted for OpenAI-compatible model discovery")
	}
}

func TestPublicPricingPageRouteMountedWithoutAuthGate(t *testing.T) {
	// Mutation: wrapping /v1/pricing/page in API-key or session middleware
	// would return an auth error body instead of the handler's nil-deps guard.
	r := buildTestRouter(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/pricing/page", nil)

	r.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("GET /v1/pricing/page returned 404; route must be mounted")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503 from pricing page nil deps", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "registry_backend_error") {
		t.Fatalf("body=%s want pricing page handler guard, proving no auth middleware intercepted", rec.Body.String())
	}
}

func TestPublicRankingsRouteMountedWithoutAuthGate(t *testing.T) {
	// Mutation: wrapping /v1/public/rankings in API-key, session, or admin
	// middleware would return an auth error body instead of the handler's
	// nil-deps guard.
	r := buildTestRouter(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/public/rankings", nil)

	r.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("GET /v1/public/rankings returned 404; route must be mounted")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503 from public rankings nil deps", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "public_rankings_dependency_unset") {
		t.Fatalf("body=%s want public rankings handler guard, proving no auth middleware intercepted", rec.Body.String())
	}
}

func TestAdminModelCapabilitiesRouteMountedBehindAdminGate(t *testing.T) {
	r := buildTestRouter(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/v1/admin/models/42/capabilities",
		strings.NewReader(`{"capabilities":{"vision":true},"max_output_tokens":8192,"model_mode":"chat"}`))

	r.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("PUT /v1/admin/models/{id}/capabilities returned 404; route must be mounted")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503 from adminGate nil resolver", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "admin_gate_not_configured") {
		t.Fatalf("body=%s want admin_gate_not_configured proving route is behind adminGate", rec.Body.String())
	}
}

func TestAdminModelAliasBulkImportRouteMountedBehindAdminGate(t *testing.T) {
	r := buildTestRouter(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/models/aliases/bulk-import",
		strings.NewReader(`{"aliases":[{"tenant_id":7,"model_id":42,"alias":"gpt-4o"}]}`))

	r.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("POST /v1/admin/models/aliases/bulk-import returned 404; route must be mounted")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503 from adminGate nil resolver", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "admin_gate_not_configured") {
		t.Fatalf("body=%s want admin_gate_not_configured proving route is behind adminGate", rec.Body.String())
	}
}

func TestAdminModelCapabilityBindingsRouteMountedBehindAdminGate(t *testing.T) {
	r := buildTestRouter(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/models/42/capability-bindings", nil)

	r.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("GET /v1/admin/models/{id}/capability-bindings returned 404; route must be mounted")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503 from adminGate nil resolver", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "admin_gate_not_configured") {
		t.Fatalf("body=%s want admin_gate_not_configured proving route is behind adminGate", rec.Body.String())
	}
}

// adminGate is the SOLE privilege barrier for the tenant-policy write face (the handler
// trusts the context-injected identity for actor attribution and has no auth of its own).
// buildTestRouter wires a nil admin resolver, so adminGate short-circuits to
// 503 admin_gate_not_configured BEFORE the handler. Mutation: remount the bare handler
// without adminGate → the GET/PUT handler runs and emits its own body (never
// admin_gate_not_configured) → red. This catches a gate-drop regression that would let a
// tenant flip another tenant's inherit_global_catalog (the exact privilege escalation the
// platform-admin-only design prevents).
func TestAdminTenantPolicyGetRouteMountedBehindAdminGate(t *testing.T) {
	r := buildTestRouter(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/model-registry-policy?tenant_id=7", nil)

	r.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("GET /v1/admin/model-registry-policy returned 404; route must be mounted")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503 from adminGate nil resolver", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "admin_gate_not_configured") {
		t.Fatalf("body=%s want admin_gate_not_configured proving route is behind adminGate", rec.Body.String())
	}
}

func TestAdminTenantPolicySetRouteMountedBehindAdminGate(t *testing.T) {
	r := buildTestRouter(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/v1/admin/model-registry-policy?tenant_id=7",
		strings.NewReader(`{"inherit_global_catalog":true}`))

	r.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("PUT /v1/admin/model-registry-policy returned 404; route must be mounted")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503 from adminGate nil resolver", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "admin_gate_not_configured") {
		t.Fatalf("body=%s want admin_gate_not_configured proving route is behind adminGate", rec.Body.String())
	}
}
