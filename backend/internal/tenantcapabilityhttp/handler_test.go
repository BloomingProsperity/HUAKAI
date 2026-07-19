package tenantcapabilityhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/tenantcapability"
)

func TestSetCapabilityRequiresPlatformAdminAndWritesActor(t *testing.T) {
	store := &storeStub{setResult: tenantcapability.SetResult{
		Changed: true,
		Grant:   tenantcapability.Grant{TenantID: 7, Capability: tenantcapability.AdvancedAccountIntake, Enabled: true},
	}}
	handler := testHandler(authStub{identity: admin.AdminIdentity{TokenID: 9, Role: admin.RolePlatformAdmin}}, store)
	req := httptest.NewRequest(http.MethodPut, "/admin/v1/tenant-capabilities/7/advanced_account_intake", strings.NewReader(`{"enabled":true,"reason":"授权租户导入上游账号"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.setCalls != 1 || store.lastSet.TenantID != 7 || store.lastSet.Actor != "admin_token:9" ||
		store.lastSet.ActorRole != admin.RolePlatformAdmin || store.lastSet.Capability != tenantcapability.AdvancedAccountIntake {
		t.Fatalf("set=%+v calls=%d", store.lastSet, store.setCalls)
	}

	denied := testHandler(authStub{identity: admin.AdminIdentity{TokenID: 10, Role: admin.RoleTenantOperator, ScopeTenantID: 7}}, store)
	req = httptest.NewRequest(http.MethodPut, "/admin/v1/tenant-capabilities/7/advanced_account_intake", strings.NewReader(`{"enabled":true,"reason":"越级"}`))
	rec = httptest.NewRecorder()
	denied.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || store.setCalls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", rec.Code, store.setCalls, rec.Body.String())
	}
}

func TestListCapabilityIsPlatformOnly(t *testing.T) {
	store := &storeStub{list: []tenantcapability.Grant{{
		TenantID: 7, Capability: tenantcapability.AdvancedAccountIntake, Enabled: false,
	}}}
	handler := testHandler(authStub{identity: admin.AdminIdentity{UserID: 3, Source: admin.AdminSourceSession, Role: admin.RolePlatformAdmin}}, store)
	req := httptest.NewRequest(http.MethodGet, "/admin/v1/tenant-capabilities/?tenant_id=7", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || store.listCalls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", rec.Code, store.listCalls, rec.Body.String())
	}
	var body struct {
		Items []tenantcapability.Grant `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Items) != 1 || body.Items[0].Enabled {
		t.Fatalf("body=%s err=%v", rec.Body.String(), err)
	}
}

type authStub struct {
	identity admin.AdminIdentity
	err      error
}

func (s authStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return s.identity, s.err
}

type storeStub struct {
	list      []tenantcapability.Grant
	listErr   error
	setResult tenantcapability.SetResult
	setErr    error
	lastSet   tenantcapability.SetInput
	listCalls int
	setCalls  int
}

func (s *storeStub) List(context.Context, int64) ([]tenantcapability.Grant, error) {
	s.listCalls++
	return s.list, s.listErr
}

func (s *storeStub) Set(_ context.Context, in tenantcapability.SetInput) (tenantcapability.SetResult, error) {
	s.setCalls++
	s.lastSet = in
	return s.setResult, s.setErr
}

func testHandler(auth AdminAuth, store Store) http.Handler {
	r := chi.NewRouter()
	r.Route("/admin/v1/tenant-capabilities", func(r chi.Router) {
		Mount(r, Deps{Auth: auth, Store: store})
	})
	return r
}
