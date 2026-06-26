package tlsfphttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/tlsfpadmin"
)

type mockAuth struct {
	ident admin.AdminIdentity
	err   error
}

func (m mockAuth) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return m.ident, m.err
}

type mockSvc struct {
	profile   tlsfpadmin.Profile
	profiles  []tlsfpadmin.Profile
	getErr    error
	createErr error
	updateErr error
	setErr    error
	deleteErr error
}

func (m mockSvc) List(context.Context, int64) ([]tlsfpadmin.Profile, error) {
	return m.profiles, m.getErr
}
func (m mockSvc) Get(context.Context, int64, int64) (tlsfpadmin.Profile, error) {
	return m.profile, m.getErr
}
func (m mockSvc) Create(context.Context, tlsfpadmin.CreateInput) (tlsfpadmin.Profile, error) {
	return m.profile, m.createErr
}
func (m mockSvc) Update(context.Context, tlsfpadmin.UpdateInput) (tlsfpadmin.Profile, error) {
	return m.profile, m.updateErr
}
func (m mockSvc) SetStatus(context.Context, tlsfpadmin.SetStatusInput) (tlsfpadmin.Profile, error) {
	return m.profile, m.setErr
}
func (m mockSvc) Delete(context.Context, int64, int64) error { return m.deleteErr }

func adminDeps(svc Service) AdminDeps {
	return AdminDeps{Auth: mockAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin, TokenID: 1}}, Service: svc}
}

func do(d AdminDeps, method, target, body string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Route("/p", func(r chi.Router) { MountTLSFPAdminRoutes(r, d) })
	req := httptest.NewRequest(method, "/p"+target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestList_MissingTenantID_Returns400(t *testing.T) {
	if rec := do(adminDeps(mockSvc{}), "GET", "/", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d; want 400", rec.Code)
	}
}

func TestGet_NilDeps_Returns503(t *testing.T) {
	if rec := do(AdminDeps{}, "GET", "/5?tenant_id=1", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d; want 503", rec.Code)
	}
}

func TestGet_UnauthorizedToken_Returns401(t *testing.T) {
	d := AdminDeps{Auth: mockAuth{err: errors.New("bad token")}, Service: mockSvc{}}
	if rec := do(d, "GET", "/5?tenant_id=1", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d; want 401", rec.Code)
	}
}

func TestGet_TenantOperatorRole_Returns403(t *testing.T) {
	d := AdminDeps{Auth: mockAuth{ident: admin.AdminIdentity{Role: admin.RoleTenantOperator}}, Service: mockSvc{}}
	if rec := do(d, "GET", "/5?tenant_id=1", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d; want 403", rec.Code)
	}
}

func TestCreate_UnknownField_Returns400(t *testing.T) {
	if rec := do(adminDeps(mockSvc{}), "POST", "/", `{"tenant_id":1,"name":"x","bogus":true}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d; want 400 (DisallowUnknownFields)", rec.Code)
	}
}

func TestCreate_TrailingJSONReturns400(t *testing.T) {
	// 严格 JSON 只接受一个对象。Mutation:只 Decode 一次不验 EOF,这个 body
	// 会创建成功并静默忽略第二个对象。
	body := `{"tenant_id":1,"name":"x"}{"tenant_id":1,"name":"y"}`
	if rec := do(adminDeps(mockSvc{profile: tlsfpadmin.Profile{ID: 42}}), "POST", "/", body); rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d; want 400 for trailing JSON body=%s", rec.Code, rec.Body.String())
	}
}

// PUT 不得接受 status 字段(status 只走 POST /{id}/status)。
func TestUpdate_StatusInBody_Returns400(t *testing.T) {
	if rec := do(adminDeps(mockSvc{}), "PUT", "/5?tenant_id=1", `{"name":"x","status":"disabled"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d; want 400 (status is an unknown field in the PUT body)", rec.Code)
	}
}

func TestCreate_HappyPath_Returns201(t *testing.T) {
	rec := do(adminDeps(mockSvc{profile: tlsfpadmin.Profile{ID: 42}}), "POST", "/", `{"tenant_id":1,"name":"x"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("code = %d; want 201", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"id":42`) {
		t.Fatalf("body missing profile id 42: %s", rec.Body.String())
	}
}

func TestDelete_NotFound_Returns404(t *testing.T) {
	if rec := do(adminDeps(mockSvc{deleteErr: tlsfpadmin.ErrNotFound}), "DELETE", "/5?tenant_id=1", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d; want 404", rec.Code)
	}
}

func TestSetStatus_InvalidStatus_Returns400(t *testing.T) {
	rec := do(adminDeps(mockSvc{setErr: tlsfpadmin.ErrInvalidStatus}), "POST", "/5/status?tenant_id=1", `{"status":"drift_detected"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d; want 400", rec.Code)
	}
}

func TestDelete_HappyPath_Returns200WithDeletedTrue(t *testing.T) {
	rec := do(adminDeps(mockSvc{}), "DELETE", "/5?tenant_id=1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d; want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"deleted":true`) {
		t.Fatalf("body missing deleted:true: %s", rec.Body.String())
	}
}
