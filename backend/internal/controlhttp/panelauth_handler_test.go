// HUAKAI · iKun

package controlhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/panelauth"
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

// 守 admin 账号 → panel=admin, 且解析用的 tenant/user 取自 session(非伪造)。
// mutation: handler 把 ident.UserID 传错/写死 → gotUser 断言红; resolver 返回未被透传 → panel 断言红。
func TestAuthMe_AdminRole(t *testing.T) {
	res := &mockResolver{panel: panelauth.PanelAdmin}
	w, body := serve(AuthMeDeps{Resolver: res}, &sessionauth.SessionIdentity{TenantID: 5, UserID: 100})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", w.Code)
	}
	if body["panel"] != "admin" || body["user_id"] != float64(100) || body["tenant_id"] != float64(5) {
		t.Fatalf("body=%v, want panel=admin user_id=100 tenant_id=5", body)
	}
	if !res.called || res.gotTenant != 5 || res.gotUser != 100 {
		t.Fatalf("resolver called with (%d,%d), want (5,100) from session", res.gotTenant, res.gotUser)
	}
}

// 守普通账号 → panel=user(证明面板随后端解析变, 非写死)。
func TestAuthMe_UserRole(t *testing.T) {
	w, body := serve(AuthMeDeps{Resolver: &mockResolver{panel: panelauth.PanelUser}}, &sessionauth.SessionIdentity{TenantID: 7, UserID: 200})
	if w.Code != http.StatusOK || body["panel"] != "user" {
		t.Fatalf("status=%d panel=%v, want 200/user", w.Code, body["panel"])
	}
}

// 守未认证: 无 session → 401, 且不触达 resolver(未登录拿不到任何面板)。
// mutation: handler 删掉 SessionFromContext 检查 → 会以零值 tenant/user 调 resolver → resolver.called=true → 红。
func TestAuthMe_NoSession(t *testing.T) {
	res := &mockResolver{panel: panelauth.PanelUser}
	w, _ := serve(AuthMeDeps{Resolver: res}, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no-session status=%d, want 401", w.Code)
	}
	if res.called {
		t.Fatal("resolver must NOT run without an authenticated session")
	}
}

// 守账号已注销: session 有效但用户行不存在/软删(ErrUserNotFound)→ 403, 不发任何面板。
// mutation: handler 把 ErrUserNotFound fallback 成某面板/200 → 红。
func TestAuthMe_AccountNotActive(t *testing.T) {
	w, body := serve(AuthMeDeps{Resolver: &mockResolver{err: panelauth.ErrUserNotFound}}, &sessionauth.SessionIdentity{TenantID: 5, UserID: 999})
	if w.Code != http.StatusForbidden {
		t.Fatalf("deleted-account status=%d, want 403", w.Code)
	}
	if _, hasPanel := body["panel"]; hasPanel {
		t.Fatalf("must not return a panel for an inactive account, body=%v", body)
	}
}

// 守后端瞬态错误 → 503(不 fallback 成面板)。
func TestAuthMe_BackendError(t *testing.T) {
	w, _ := serve(AuthMeDeps{Resolver: &mockResolver{err: context.DeadlineExceeded}}, &sessionauth.SessionIdentity{TenantID: 5, UserID: 100})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("backend error status=%d, want 503", w.Code)
	}
}

// 守 nil resolver → 503, 不 panic。
func TestAuthMe_NilResolver(t *testing.T) {
	w, _ := serve(AuthMeDeps{Resolver: nil}, &sessionauth.SessionIdentity{TenantID: 5, UserID: 100})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil resolver status=%d, want 503", w.Code)
	}
}
