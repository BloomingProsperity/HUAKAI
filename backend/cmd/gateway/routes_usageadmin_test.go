package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

func TestTenantOperatorForbiddenOnPlatformUsageOverviewButReachesTenantOverview(t *testing.T) {
	r := chi.NewRouter()
	resolver := fakeAdminResolver{id: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7}}
	mountUsageAdminRoutesResolved(r, &deps{}, resolver)

	oldRec := httptest.NewRecorder()
	r.ServeHTTP(oldRec, httptest.NewRequest(http.MethodGet, "/v1/admin/usage/overview?window=24h", nil))
	if oldRec.Code != http.StatusForbidden {
		t.Fatalf("旧平台总览对租户管理员应为 403，实得 %d 体=%s", oldRec.Code, oldRec.Body.String())
	}
	if !strings.Contains(oldRec.Body.String(), "admin_forbidden_scope") {
		t.Fatalf("旧平台总览体=%s 必须证明仍经 adminGate", oldRec.Body.String())
	}

	newRec := httptest.NewRecorder()
	r.ServeHTTP(newRec, httptest.NewRequest(http.MethodGet, "/admin/v1/usage/overview?window=24h", nil))
	// billingQueries 为空时双角色 handler 503；若误包 adminGate 则会 403。
	if newRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("租户总览应越过 adminGate 进入双角色 handler，实得 %d 体=%s", newRec.Code, newRec.Body.String())
	}
	if strings.Contains(newRec.Body.String(), "admin_forbidden_scope") {
		t.Fatalf("租户总览被 adminGate 拒掉了：%s", newRec.Body.String())
	}
	if !strings.Contains(newRec.Body.String(), "gateway_not_configured") {
		t.Fatalf("租户总览体=%s 必须到达 NewTenantOverviewHandler", newRec.Body.String())
	}
}

func TestPlatformAdminMissingTenantIDOnTenantUsageOverview(t *testing.T) {
	r := chi.NewRouter()
	resolver := fakeAdminResolver{id: admin.AdminIdentity{Role: admin.RolePlatformAdmin}}
	mountUsageAdminRoutesResolved(r, &deps{}, resolver)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/usage/overview?window=24h", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("部署者省略 tenant_id 应为 400，实得 %d 体=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "tenant_id_required") {
		t.Fatalf("体=%s 必须含 tenant_id_required，禁止回落全平台", rec.Body.String())
	}
}

func TestTenantOperatorForbiddenOnPlatformUsageOverviewButReachesTenantHourly(t *testing.T) {
	r := chi.NewRouter()
	resolver := fakeAdminResolver{id: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7}}
	mountUsageAdminRoutesResolved(r, &deps{}, resolver)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/usage/hourly?window=24h", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("租户小时趋势应越过 adminGate 进入双角色 handler，实得 %d 体=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "admin_forbidden_scope") {
		t.Fatalf("租户小时趋势被 adminGate 拒掉了：%s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "gateway_not_configured") {
		t.Fatalf("租户小时趋势体=%s 必须到达 NewTenantHourlyHandler", rec.Body.String())
	}
}

func TestPlatformAdminMissingTenantIDOnTenantUsageHourly(t *testing.T) {
	r := chi.NewRouter()
	resolver := fakeAdminResolver{id: admin.AdminIdentity{Role: admin.RolePlatformAdmin}}
	mountUsageAdminRoutesResolved(r, &deps{}, resolver)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/v1/usage/hourly?window=24h", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("部署者省略 tenant_id 应为 400，实得 %d 体=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "tenant_id_required") {
		t.Fatalf("体=%s 必须含 tenant_id_required，禁止回落全平台", rec.Body.String())
	}
}
