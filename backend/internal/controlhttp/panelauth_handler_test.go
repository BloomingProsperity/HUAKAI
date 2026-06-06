// HUAKAI · iKun

package controlhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/panelauth"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

type mockResolver struct {
	panel     panelauth.Panel
	err       error
	called    bool
	gotTenant int64
	gotUser   int64
}

func (m *mockResolver) PanelForUser(_ context.Context, tenantID, userID int64) (panelauth.Panel, error) {
	m.called = true
	m.gotTenant, m.gotUser = tenantID, userID
	return m.panel, m.err
}

type profileKey struct {
	tenantID int64
	userID   int64
}

type mockProfileService struct {
	users          map[profileKey]userauth.User
	getErr         error
	updateErr      error
	getCalls       int
	updateCalls    int
	gotTenant      int64
	gotUser        int64
	gotDisplayName string
}

func (m *mockProfileService) GetProfile(_ context.Context, tenantID, userID int64) (userauth.User, error) {
	m.getCalls++
	m.gotTenant, m.gotUser = tenantID, userID
	if m.getErr != nil {
		return userauth.User{}, m.getErr
	}
	if user, ok := m.users[profileKey{tenantID: tenantID, userID: userID}]; ok {
		return user, nil
	}
	return userauth.User{}, userauth.ErrUserNotFound
}

func (m *mockProfileService) UpdateProfile(_ context.Context, tenantID, userID int64, displayName string) (userauth.User, error) {
	m.updateCalls++
	m.gotTenant, m.gotUser, m.gotDisplayName = tenantID, userID, displayName
	if m.updateErr != nil {
		return userauth.User{}, m.updateErr
	}
	user := userauth.User{ID: userID, TenantID: tenantID, DisplayName: strings.TrimSpace(displayName)}
	if m.users == nil {
		m.users = map[profileKey]userauth.User{}
	}
	m.users[profileKey{tenantID: tenantID, userID: userID}] = user
	return user, nil
}

func serve(d AuthMeDeps, ident *sessionauth.SessionIdentity) (*httptest.ResponseRecorder, map[string]any) {
	h := newAuthMeHandler(d)
	r := httptest.NewRequest(http.MethodGet, "/v1/auth/me", nil)
	if ident != nil {
		r = r.WithContext(sessionauth.ContextWithSession(r.Context(), *ident))
	}
	w := httptest.NewRecorder()
	h(w, r)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w, body
}

func serveAuthProfilePut(d AuthMeDeps, ident *sessionauth.SessionIdentity, body string) (*httptest.ResponseRecorder, map[string]any) {
	router := chi.NewRouter()
	MountAuthMeRoutes(router, d)
	r := httptest.NewRequest(http.MethodPut, "/me/profile", strings.NewReader(body))
	if ident != nil {
		r = r.WithContext(sessionauth.ContextWithSession(r.Context(), *ident))
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w, resp
}

// 守 admin 账号 → panel=admin, 且解析用的 tenant/user 取自 session(非伪造)。
// mutation: handler 把 ident.UserID 传错/写死 → gotUser 断言红; resolver 返回未被透传 → panel 断言红。
func TestAuthMe_AdminRole(t *testing.T) {
	res := &mockResolver{panel: panelauth.PanelAdmin}
	profiles := &mockProfileService{users: map[profileKey]userauth.User{
		profileKey{tenantID: 5, userID: 100}: {ID: 100, TenantID: 5, DisplayName: "Root Admin"},
	}}
	w, body := serve(AuthMeDeps{Resolver: res, Profiles: profiles}, &sessionauth.SessionIdentity{TenantID: 5, UserID: 100})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", w.Code)
	}
	if body["panel"] != "admin" || body["user_id"] != float64(100) || body["tenant_id"] != float64(5) || body["display_name"] != "Root Admin" {
		t.Fatalf("body=%v, want panel=admin user_id=100 tenant_id=5 display_name=Root Admin", body)
	}
	if !res.called || res.gotTenant != 5 || res.gotUser != 100 {
		t.Fatalf("resolver called with (%d,%d), want (5,100) from session", res.gotTenant, res.gotUser)
	}
	if profiles.gotTenant != 5 || profiles.gotUser != 100 {
		t.Fatalf("profile lookup called with (%d,%d), want (5,100) from session", profiles.gotTenant, profiles.gotUser)
	}
}

// 守普通账号 → panel=user(证明面板随后端解析变, 非写死)。
func TestAuthMe_UserRole(t *testing.T) {
	profiles := &mockProfileService{users: map[profileKey]userauth.User{
		profileKey{tenantID: 7, userID: 200}: {ID: 200, TenantID: 7, DisplayName: "Normal User"},
	}}
	w, body := serve(AuthMeDeps{Resolver: &mockResolver{panel: panelauth.PanelUser}, Profiles: profiles}, &sessionauth.SessionIdentity{TenantID: 7, UserID: 200})
	if w.Code != http.StatusOK || body["panel"] != "user" {
		t.Fatalf("status=%d panel=%v, want 200/user", w.Code, body["panel"])
	}
}

// 守未认证: 无 session → 401, 且不触达 resolver(未登录拿不到任何面板)。
// mutation: handler 删掉 SessionFromContext 检查 → 会以零值 tenant/user 调 resolver → resolver.called=true → 红。
func TestAuthMe_NoSession(t *testing.T) {
	res := &mockResolver{panel: panelauth.PanelUser}
	profiles := &mockProfileService{}
	w, _ := serve(AuthMeDeps{Resolver: res, Profiles: profiles}, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no-session status=%d, want 401", w.Code)
	}
	if res.called {
		t.Fatal("resolver must NOT run without an authenticated session")
	}
	if profiles.getCalls != 0 {
		t.Fatalf("profile lookup calls=%d want 0 without session", profiles.getCalls)
	}
}

// 守账号已注销: session 有效但用户行不存在/软删(ErrUserNotFound)→ 403, 不发任何面板。
// mutation: handler 把 ErrUserNotFound fallback 成某面板/200 → 红。
func TestAuthMe_AccountNotActive(t *testing.T) {
	w, body := serve(AuthMeDeps{Resolver: &mockResolver{err: panelauth.ErrUserNotFound}, Profiles: &mockProfileService{}}, &sessionauth.SessionIdentity{TenantID: 5, UserID: 999})
	if w.Code != http.StatusForbidden {
		t.Fatalf("deleted-account status=%d, want 403", w.Code)
	}
	if _, hasPanel := body["panel"]; hasPanel {
		t.Fatalf("must not return a panel for an inactive account, body=%v", body)
	}
}

// 守后端瞬态错误 → 503(不 fallback 成面板)。
func TestAuthMe_BackendError(t *testing.T) {
	w, _ := serve(AuthMeDeps{Resolver: &mockResolver{err: context.DeadlineExceeded}, Profiles: &mockProfileService{}}, &sessionauth.SessionIdentity{TenantID: 5, UserID: 100})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("backend error status=%d, want 503", w.Code)
	}
}

// 守 nil resolver → 503, 不 panic。
func TestAuthMe_NilResolver(t *testing.T) {
	w, _ := serve(AuthMeDeps{Resolver: nil, Profiles: &mockProfileService{}}, &sessionauth.SessionIdentity{TenantID: 5, UserID: 100})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil resolver status=%d, want 503", w.Code)
	}
}

// 守自助资料更新只取 session scope, 请求体里的 tenant_id/user_id 必须是无效噪声。
// MUTATION: handler 从 body/query 读取 tenant_id/user_id 传给 service -> got scope 变成 8/999, 本测试红。
func TestUpdateProfile_SelfScoped(t *testing.T) {
	res := &mockResolver{panel: panelauth.PanelUser}
	profiles := &mockProfileService{users: map[profileKey]userauth.User{
		profileKey{tenantID: 7, userID: 42}:  {ID: 42, TenantID: 7, DisplayName: "Before"},
		profileKey{tenantID: 8, userID: 999}: {ID: 999, TenantID: 8, DisplayName: "Other Tenant"},
	}}
	body := `{"tenant_id":8,"user_id":999,"display_name":" Alice Updated "}`
	w, resp := serveAuthProfilePut(AuthMeDeps{Resolver: res, Profiles: profiles}, &sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if profiles.gotTenant != 7 || profiles.gotUser != 42 {
		t.Fatalf("update scope=(%d,%d), want session (7,42)", profiles.gotTenant, profiles.gotUser)
	}
	if profiles.gotDisplayName != " Alice Updated " {
		t.Fatalf("service display_name arg=%q, want raw body value for service validation", profiles.gotDisplayName)
	}
	if resp["display_name"] != "Alice Updated" || resp["user_id"] != float64(42) || resp["tenant_id"] != float64(7) {
		t.Fatalf("response=%v, want updated self profile", resp)
	}
	if other := profiles.users[profileKey{tenantID: 8, userID: 999}].DisplayName; other != "Other Tenant" {
		t.Fatalf("other tenant display_name=%q, want unchanged", other)
	}
}

// 守 PUT 后 GET /me 读取同一 profile source, 证明更新不是 no-op。
// MUTATION: PUT handler returns 200 without persisting -> following GET still returns Before, 本测试红。
func TestUpdateProfile_PersistsIntoAuthMe(t *testing.T) {
	res := &mockResolver{panel: panelauth.PanelUser}
	profiles := &mockProfileService{users: map[profileKey]userauth.User{
		profileKey{tenantID: 7, userID: 42}: {ID: 42, TenantID: 7, DisplayName: "Before"},
	}}
	deps := AuthMeDeps{Resolver: res, Profiles: profiles}
	w, _ := serveAuthProfilePut(deps, &sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, `{"display_name":"After"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", w.Code, w.Body.String())
	}
	getW, body := serve(deps, &sessionauth.SessionIdentity{TenantID: 7, UserID: 42})
	if getW.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", getW.Code, getW.Body.String())
	}
	if body["display_name"] != "After" {
		t.Fatalf("GET display_name=%v want After", body["display_name"])
	}
}

// 守未认证不能写资料。
// MUTATION: handler deletes SessionFromContext check -> service updateCalls becomes 1, 本测试红。
func TestUpdateProfile_AuthRequired(t *testing.T) {
	profiles := &mockProfileService{}
	w, _ := serveAuthProfilePut(AuthMeDeps{Resolver: &mockResolver{panel: panelauth.PanelUser}, Profiles: profiles}, nil, `{"display_name":"No Session"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", w.Code)
	}
	if profiles.updateCalls != 0 {
		t.Fatalf("updateCalls=%d want 0 without session", profiles.updateCalls)
	}
}

// 守 service validation error maps to HTTP 400 and does not get hidden as 200/503.
// MUTATION: handler skips validation error mapping -> status is not 400, 本测试红。
func TestUpdateProfile_Validation(t *testing.T) {
	profiles := &mockProfileService{updateErr: userauth.ErrInvalidInput}
	w, _ := serveAuthProfilePut(AuthMeDeps{Resolver: &mockResolver{panel: panelauth.PanelUser}, Profiles: profiles}, &sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, `{"display_name":" "}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", w.Code, w.Body.String())
	}
}
