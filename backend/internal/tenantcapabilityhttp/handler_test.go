package tenantcapabilityhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/tenantcapability"
)

type authStub struct {
	identity admin.AdminIdentity
	err      error
}

func (s authStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return s.identity, s.err
}

type serviceStub struct {
	mutation  tenantcapability.Mutation
	setCalls  int
	listID    int64
	listCalls int
}

func (s *serviceStub) List(_ context.Context, tenantID int64) ([]tenantcapability.Grant, error) {
	s.listCalls++
	s.listID = tenantID
	return []tenantcapability.Grant{{TenantID: tenantID, Capability: tenantcapability.ClaudeCookie, Status: "granted"}}, nil
}

func (s *serviceStub) Set(_ context.Context, mutation tenantcapability.Mutation) (tenantcapability.Grant, error) {
	s.setCalls++
	s.mutation = mutation
	return tenantcapability.Grant{TenantID: mutation.TenantID, Capability: mutation.Capability, Status: "granted"}, nil
}

func TestDeploymentTokenCanGrantAndListTenantCapability(t *testing.T) {
	service := &serviceStub{}
	handler := testRouter(authStub{identity: admin.AdminIdentity{
		Source: admin.AdminSourceToken, TokenID: 4, Role: admin.RolePlatformAdmin,
	}}, service)
	recorder := request(handler, http.MethodPut, "/admin/v1/tenant-capabilities/account_intake.claude_cookie",
		`{"tenant_id":7,"enabled":true,"reason":"批准租户接入 Claude Cookie"}`)
	if recorder.Code != http.StatusOK || service.setCalls != 1 || service.mutation.TenantID != 7 ||
		service.mutation.Capability != tenantcapability.ClaudeCookie || service.mutation.ActorID != "admin_token:4" {
		t.Fatalf("status=%d calls=%d mutation=%+v body=%s", recorder.Code, service.setCalls, service.mutation, recorder.Body.String())
	}
	recorder = request(handler, http.MethodGet, "/admin/v1/tenant-capabilities/?tenant_id=7", "")
	if recorder.Code != http.StatusOK || service.listCalls != 1 || service.listID != 7 {
		t.Fatalf("status=%d calls=%d tenant=%d body=%s", recorder.Code, service.listCalls, service.listID, recorder.Body.String())
	}
}

func TestTenantCapabilityGovernanceRejectsSessionAndTenantOperator(t *testing.T) {
	identities := []admin.AdminIdentity{
		{Source: admin.AdminSourceSession, UserID: 3, Role: admin.RolePlatformAdmin},
		{Source: admin.AdminSourceToken, TokenID: 4, Role: admin.RoleTenantOperator, ScopeTenantID: 7},
	}
	for _, identity := range identities {
		service := &serviceStub{}
		handler := testRouter(authStub{identity: identity}, service)
		recorder := request(handler, http.MethodPut, "/admin/v1/tenant-capabilities/account_sync.crs",
			`{"tenant_id":7,"enabled":true,"reason":"批准租户执行 CRS 账号同步"}`)
		if recorder.Code != http.StatusForbidden || service.setCalls != 0 {
			t.Fatalf("identity=%+v status=%d calls=%d body=%s", identity, recorder.Code, service.setCalls, recorder.Body.String())
		}
	}
}

func TestTenantCapabilityMutationStrictlyValidatesBody(t *testing.T) {
	service := &serviceStub{}
	handler := testRouter(authStub{identity: admin.AdminIdentity{
		Source: admin.AdminSourceToken, TokenID: 4, Role: admin.RolePlatformAdmin,
	}}, service)
	for _, body := range []string{
		`{"tenant_id":7,"reason":"缺少 enabled"}`,
		`{"tenant_id":7,"enabled":true,"reason":"合法长度但含未知字段","unknown":true}`,
	} {
		recorder := request(handler, http.MethodPut, "/admin/v1/tenant-capabilities/account_bundle.structure", body)
		if recorder.Code != http.StatusBadRequest || service.setCalls != 0 {
			t.Fatalf("status=%d calls=%d body=%s", recorder.Code, service.setCalls, recorder.Body.String())
		}
	}
}

func testRouter(auth AdminAuth, service Service) http.Handler {
	router := chi.NewRouter()
	router.Route("/admin/v1/tenant-capabilities", func(router chi.Router) { Mount(router, Deps{Auth: auth, Service: service}) })
	return router
}

func request(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}
