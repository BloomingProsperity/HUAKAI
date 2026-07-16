package riskoverviewhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

type fakeAuth struct {
	ident admin.AdminIdentity
	err   error
}

func (a fakeAuth) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if a.err != nil {
		return admin.AdminIdentity{}, a.err
	}
	return a.ident, nil
}

type fakeStore struct {
	gotTenant int64
	called    bool
	ov        Overview
}

func (s *fakeStore) Overview(_ context.Context, tenantID int64) (Overview, error) {
	s.called = true
	s.gotTenant = tenantID
	return s.ov, nil
}

func mount(auth AdminAuth, store Store) http.Handler {
	r := chi.NewRouter()
	MountAdminRoutes(r, AdminDeps{Auth: auth, Store: store})
	return r
}

func do(h http.Handler, url string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	return rec
}

func TestOverviewPlatformAdminPassesResolvedTenant(t *testing.T) {
	store := &fakeStore{ov: Overview{DisabledKeys: 3, FiringAlerts: 1, DisabledUsers: 2, IPBlacklistedKeys: 4}}
	h := mount(fakeAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}}, store)

	rec := do(h, "/admin/v1/risk/overview?tenant_id=7")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码=%d 期望 200;体=%s", rec.Code, rec.Body.String())
	}
	if !store.called || store.gotTenant != 7 {
		t.Fatalf("store 应按 tenant=7 调用,实得 called=%v tenant=%d", store.called, store.gotTenant)
	}
	var resp overviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析: %v", err)
	}
	if resp.TenantID != 7 || resp.DisabledKeys != 3 || resp.FiringAlerts != 1 || resp.DisabledUsers != 2 || resp.IPBlacklistedKeys != 4 {
		t.Fatalf("响应=%+v 与 store 不符", resp)
	}
}

func TestOverviewTenantOperatorDefaultsToOwnScope(t *testing.T) {
	store := &fakeStore{}
	h := mount(fakeAuth{ident: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7}}, store)

	rec := do(h, "/admin/v1/risk/overview") // 不传 tenant_id
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码=%d 期望 200", rec.Code)
	}
	if store.gotTenant != 7 {
		t.Fatalf("租户运营者应回退自身 scope=7,实得 %d", store.gotTenant)
	}
}

// IDOR 守卫:租户运营者请求**他人** tenant_id 必 403,且**绝不触达 store**(防跨租户读)。
func TestOverviewTenantOperatorCannotReadOtherTenant(t *testing.T) {
	store := &fakeStore{}
	h := mount(fakeAuth{ident: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7}}, store)

	rec := do(h, "/admin/v1/risk/overview?tenant_id=9") // 越权请求他人租户
	if rec.Code != http.StatusForbidden {
		t.Fatalf("状态码=%d 期望 403(跨租户必拒)", rec.Code)
	}
	if store.called {
		t.Fatal("跨租户越权时 store 不应被调用(IDOR 守卫失效)")
	}
}

func TestOverviewPlatformAdminRequiresTenantID(t *testing.T) {
	store := &fakeStore{}
	h := mount(fakeAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}}, store)

	rec := do(h, "/admin/v1/risk/overview") // 平台 admin 不传 tenant_id
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码=%d 期望 400(平台 admin 必须显式传 tenant_id)", rec.Code)
	}
	if store.called {
		t.Fatal("缺 tenant_id 时不应调 store")
	}
}

func TestOverviewAuthFailureRejected(t *testing.T) {
	store := &fakeStore{}
	h := mount(fakeAuth{err: admin.ErrAdminUnauthorized}, store)

	rec := do(h, "/admin/v1/risk/overview?tenant_id=7")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("状态码=%d 期望 401", rec.Code)
	}
	if store.called {
		t.Fatal("鉴权失败时不应调 store")
	}
}

func TestOverviewStoreUnconfiguredReturns503(t *testing.T) {
	h := mount(fakeAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin}}, nil)
	rec := do(h, "/admin/v1/risk/overview?tenant_id=7")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码=%d 期望 503(Store 未配)", rec.Code)
	}
}
