package tenantadminhttp

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauthtest"
	"github.com/BloomingProsperity/HUAKAI/internal/tenantadmin"
)

type tenantAdminAuthStub struct {
	identity admin.AdminIdentity
	err      error
}

func (s tenantAdminAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return s.identity, s.err
}

type tenantAdminServiceStub struct {
	listCalls   int
	createCalls int
	statusCalls int
	deleteCalls int
	createInput tenantadmin.CreateInput
	statusInput tenantadmin.StatusInput
	deleteInput tenantadmin.DeleteInput
	err         error
}

func (s *tenantAdminServiceStub) List(context.Context) ([]tenantadmin.Tenant, error) {
	s.listCalls++
	return []tenantadmin.Tenant{{ID: 7, Name: "下级租户", Status: tenantadmin.StatusActive, Version: 1}}, s.err
}

func (s *tenantAdminServiceStub) Get(context.Context, int64) (tenantadmin.Tenant, error) {
	return tenantadmin.Tenant{ID: 7, Name: "下级租户", Status: tenantadmin.StatusActive, Version: 1}, s.err
}

func (s *tenantAdminServiceStub) Create(_ context.Context, input tenantadmin.CreateInput) (tenantadmin.CreateResult, error) {
	s.createCalls++
	s.createInput = input
	return tenantadmin.CreateResult{
		Tenant:       tenantadmin.Tenant{ID: 8, Name: input.Name, Status: tenantadmin.StatusActive, Version: 1},
		FirstAdminID: 81,
	}, s.err
}

func (s *tenantAdminServiceStub) SetStatus(_ context.Context, input tenantadmin.StatusInput) (tenantadmin.StatusResult, error) {
	s.statusCalls++
	s.statusInput = input
	return tenantadmin.StatusResult{
		Tenant:  tenantadmin.Tenant{ID: input.TenantID, Status: input.Status, Version: input.ExpectedVersion + 1},
		Changed: true,
	}, s.err
}

func (s *tenantAdminServiceStub) InspectDelete(context.Context, int64) (tenantadmin.DeleteImpact, error) {
	return tenantadmin.DeleteImpact{TenantID: 7, TenantVersion: 2, TenantStatus: tenantadmin.StatusDisabled, ImpactHash: "impact"}, s.err
}

func (s *tenantAdminServiceStub) Delete(_ context.Context, input tenantadmin.DeleteInput) (tenantadmin.DeleteResult, error) {
	s.deleteCalls++
	s.deleteInput = input
	return tenantadmin.DeleteResult{
		Tenant: tenantadmin.Tenant{ID: input.TenantID, Status: tenantadmin.StatusDeleted, Version: input.ExpectedVersion + 1},
	}, s.err
}

func TestTenantLifecycleRequiresPlatformAdmin(t *testing.T) {
	service := &tenantAdminServiceStub{}
	handler := mountTenantAdminHTTP(
		tenantAdminAuthStub{identity: admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		service,
	)
	rec := invokeTenantAdminHTTP(handler, http.MethodGet, "/admin/v1/tenants", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("tenant_operator 管理租户生命周期 status=%d body=%s want 403", rec.Code, rec.Body.String())
	}
	if service.listCalls != 0 {
		t.Fatalf("越权请求仍调用 service list=%d want 0", service.listCalls)
	}
}

func TestTenantCollectionExactPathAndStrictCreateContract(t *testing.T) {
	service := &tenantAdminServiceStub{}
	handler := mountTenantAdminHTTP(
		tenantAdminAuthStub{identity: admin.AdminIdentity{
			TokenID: 305, Role: admin.RolePlatformAdmin, Source: admin.AdminSourceToken,
		}},
		service,
	)

	list := invokeTenantAdminHTTP(handler, http.MethodGet, "/admin/v1/tenants", "")
	if list.Code != http.StatusOK || service.listCalls != 1 {
		t.Fatalf("无尾斜杠租户列表 status=%d calls=%d body=%s", list.Code, service.listCalls, list.Body.String())
	}

	unknown := invokeTenantAdminHTTP(handler, http.MethodPost, "/admin/v1/tenants",
		`{"name":"租户甲","admin_email":"a@example.test","admin_password":"StrongPass!2026","reason":"创建","unexpected":true}`)
	if unknown.Code != http.StatusBadRequest || service.createCalls != 0 {
		t.Fatalf("未知字段应在 service 前拒绝 status=%d calls=%d body=%s", unknown.Code, service.createCalls, unknown.Body.String())
	}

	created := invokeTenantAdminHTTP(handler, http.MethodPost, "/admin/v1/tenants",
		`{"name":"租户甲","admin_email":"a@example.test","admin_display_name":"管理员甲","admin_password":"StrongPass!2026","reason":"创建下级租户"}`)
	if created.Code != http.StatusCreated || service.createCalls != 1 {
		t.Fatalf("创建租户 status=%d calls=%d body=%s", created.Code, service.createCalls, created.Body.String())
	}
	if service.createInput.Audit.ActorID != "admin_token:305" ||
		service.createInput.Audit.ActorRole != admin.RolePlatformAdmin ||
		service.createInput.Audit.Reason != "创建下级租户" {
		t.Fatalf("创建日志归属=%+v", service.createInput.Audit)
	}
}

func TestTenantLifecycleStableConflictErrors(t *testing.T) {
	service := &tenantAdminServiceStub{err: tenantadmin.ErrVersionConflict}
	handler := mountTenantAdminHTTP(
		tenantAdminAuthStub{identity: admin.AdminIdentity{TokenID: 305, Role: admin.RolePlatformAdmin}},
		service,
	)
	rec := invokeTenantAdminHTTP(handler, http.MethodPatch, "/admin/v1/tenants/7/status",
		`{"status":"disabled","expected_version":1,"reason":"停用"}`)
	if rec.Code != http.StatusConflict || !bytes.Contains(rec.Body.Bytes(), []byte(`"code":"tenant_version_conflict"`)) {
		t.Fatalf("版本冲突 status=%d body=%s want 409 tenant_version_conflict", rec.Code, rec.Body.String())
	}

	service.err = errors.New("database unavailable")
	rec = invokeTenantAdminHTTP(handler, http.MethodDelete, "/admin/v1/tenants/7",
		`{"expected_version":2,"impact_hash":"abc","reason":"删除"}`)
	if rec.Code != http.StatusServiceUnavailable || !bytes.Contains(rec.Body.Bytes(), []byte(`"code":"tenant_admin_failed"`)) {
		t.Fatalf("后端故障 status=%d body=%s want 503 tenant_admin_failed", rec.Code, rec.Body.String())
	}
}

func TestTenantLifecycleWritesAreSessionSafe(t *testing.T) {
	handler := mountTenantAdminHTTP(adminsessionauthtest.Resolver(), &tenantAdminServiceStub{})
	for _, testCase := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/admin/v1/tenants"},
		{method: http.MethodPatch, path: "/admin/v1/tenants/7/status"},
		{method: http.MethodDelete, path: "/admin/v1/tenants/7"},
	} {
		status := adminsessionauthtest.Status(
			handler, testCase.method, testCase.path, adminsessionauthtest.SessionBearer,
		)
		if status == http.StatusUnauthorized {
			t.Fatalf("SessionSafe 写端点 %s %s 被会话写门拒绝", testCase.method, testCase.path)
		}
	}
}

func mountTenantAdminHTTP(auth AdminAuth, service Service) http.Handler {
	router := chi.NewRouter()
	router.Route("/admin/v1/tenants", func(r chi.Router) {
		Mount(r, Deps{Auth: auth, Service: service})
	})
	return router
}

func invokeTenantAdminHTTP(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}
