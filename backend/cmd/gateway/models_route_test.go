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
