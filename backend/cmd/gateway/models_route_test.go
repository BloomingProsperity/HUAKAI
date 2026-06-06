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
