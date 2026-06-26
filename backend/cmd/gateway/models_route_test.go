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
	// 变异:把 /v1/pricing/page 包进 API-key 或 session 中间件,
	// 会返回一个 auth 错误响应体,而不是 handler 的 nil-deps 守卫。
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
	// 变异:若把 /v1/public/rankings 包进 API-key、session 或 admin
	// 中间件,会返回鉴权错误响应体,而非 handler 的
	// nil-deps 守卫。
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

// adminGate 是 tenant-policy 写入面的唯一权限屏障(handler
// 信任 context 注入的身份做 actor 归属,自身不带任何鉴权)。
// buildTestRouter 注入一个 nil 的 admin resolver,因此 adminGate 会在
// handler 之前短路返回 503 admin_gate_not_configured。变异:把裸 handler
// 不经 adminGate 重新挂载 → GET/PUT handler 运行并产出自己的响应体(绝不会是
// admin_gate_not_configured)→ 红。这能抓住「丢掉 gate」的回归,否则一个
// tenant 就能翻动另一个 tenant 的 inherit_global_catalog(正是
// platform-admin-only 设计要防的越权)。
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
