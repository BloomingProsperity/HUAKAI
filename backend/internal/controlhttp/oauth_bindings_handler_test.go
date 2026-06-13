package controlhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

// oauthBindingListerStub 记录列表查询收到的 tenant/user,并按 (tenant,user) 返回不同集合,
// 让「越权读到他人绑定」与「正确按 session 过滤」产生可区分的输出。
type oauthBindingListerStub struct {
	calls       int
	gotTenantID int64
	gotUserID   int64
	byUser      map[int64][]userauth.SocialIdentityLink
	err         error
}

func (s *oauthBindingListerStub) ListSocialIdentityLinks(_ context.Context, tenantID, userID int64) ([]userauth.SocialIdentityLink, error) {
	s.calls++
	s.gotTenantID = tenantID
	s.gotUserID = userID
	if s.err != nil {
		return nil, s.err
	}
	return s.byUser[userID], nil
}

func serveOAuthBindings(t *testing.T, deps OAuthBindingsDeps, ident sessionauth.SessionIdentity, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Route("/v1/users/me/oauth-bindings", func(r chi.Router) {
		MountOAuthBindingsRoutes(r, deps)
	})
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req = req.WithContext(sessionauth.ContextWithSession(req.Context(), ident))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

//  1. 列表只返回「session 身份」那个用户的绑定,绝不是 path/query 或他人的。
//     discriminating fixture: 用户 42 有 google,用户 99 有 github;断言响应里只有 google、没有 github,
//     且 lister 收到的 userID==42。
//     MUTATION: handler 若改从 query/path 取 user_id(=99)或忽略 session 用别的 id → lister.gotUserID!=42
//     或响应含 github → 红。
func TestOAuthBindingsListScopesToSessionUser(t *testing.T) {
	linkedAt := time.Date(2026, 6, 1, 8, 30, 0, 0, time.UTC)
	lister := &oauthBindingListerStub{byUser: map[int64][]userauth.SocialIdentityLink{
		42: {{Provider: userauth.SocialProviderGoogle, Subject: "go***le", LinkedAt: linkedAt}},
		99: {{Provider: userauth.SocialProviderGitHub, Subject: "gh***ub", LinkedAt: linkedAt}},
	}}
	rec := serveOAuthBindings(t, OAuthBindingsDeps{Bindings: lister},
		sessionauth.SessionIdentity{TenantID: 7, UserID: 42, FamilyID: "family-1"},
		http.MethodGet, "/v1/users/me/oauth-bindings?user_id=99", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if lister.calls != 1 || lister.gotTenantID != 7 || lister.gotUserID != 42 {
		t.Fatalf("list call mismatch: calls=%d tenant=%d user=%d; MUTATION: reading user_id from query (=99) makes gotUserID!=42",
			lister.calls, lister.gotTenantID, lister.gotUserID)
	}
	var body struct {
		Bindings []struct {
			Provider string `json:"provider"`
			Subject  string `json:"subject"`
			LinkedAt string `json:"linked_at"`
		} `json:"bindings"`
	}
	decodeControlBody(t, rec, &body)
	if len(body.Bindings) != 1 {
		t.Fatalf("bindings len=%d want 1 (only the session user's) body=%s", len(body.Bindings), rec.Body.String())
	}
	if body.Bindings[0].Provider != userauth.SocialProviderGoogle {
		t.Fatalf("provider=%q want google (cross-user leak would surface github)", body.Bindings[0].Provider)
	}
	if body.Bindings[0].Subject != "go***le" {
		t.Fatalf("subject=%q want masked go***le", body.Bindings[0].Subject)
	}
	if body.Bindings[0].LinkedAt == "" {
		t.Fatalf("linked_at empty; want serialized timestamp")
	}
}

//  2. 解绑指定 provider 时,handler 把 session 身份 + path provider 透传给 service,返回 unlinked=true。
//     MUTATION: handler 若漏传 provider 或从 body 取身份 → gotProvider 错 / gotUserID 错 → 红。
func TestOAuthBindingsUnlinkRemovesNamedProvider(t *testing.T) {
	svc := &authSocialLinkStub{unlinked: true}
	rec := serveOAuthBindings(t, OAuthBindingsDeps{SocialLinks: svc},
		sessionauth.SessionIdentity{TenantID: 7, UserID: 42, FamilyID: "family-1"},
		http.MethodDelete, "/v1/users/me/oauth-bindings/github", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if svc.calls != 1 || svc.gotTenantID != 7 || svc.gotUserID != 42 || svc.gotProvider != "github" {
		t.Fatalf("unlink call mismatch: calls=%d tenant=%d user=%d provider=%q want 1/7/42/github",
			svc.calls, svc.gotTenantID, svc.gotUserID, svc.gotProvider)
	}
	var body struct {
		Unlinked bool `json:"unlinked"`
	}
	decodeControlBody(t, rec, &body)
	if !body.Unlinked {
		t.Fatalf("unlinked=%v want true", body.Unlinked)
	}
}

//  3. 解绑「最后一个登录方式」(无密码且仅此绑定)时 service 返 ErrLastLoginMethod → handler 必须映射 409。
//     MUTATION: 绕过 service 末位保护(让 stub 不返 ErrLastLoginMethod)→ 期望 409 的断言变红;
//     或 handler 把 ErrLastLoginMethod 误映成 200/500 → 红。
func TestOAuthBindingsUnlinkLastLoginMethodConflict(t *testing.T) {
	svc := &authSocialLinkStub{err: userauth.ErrLastLoginMethod}
	rec := serveOAuthBindings(t, OAuthBindingsDeps{SocialLinks: svc},
		sessionauth.SessionIdentity{TenantID: 7, UserID: 42, FamilyID: "family-1"},
		http.MethodDelete, "/v1/users/me/oauth-bindings/google", "")

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409 body=%s", rec.Code, rec.Body.String())
	}
	assertControlErrorCode(t, rec, "last_login_method")
}

// 4. 未带 session → 401,且 service 一次都不被调(deny-by-default,不泄露绑定也不误删)。
func TestOAuthBindingsListRequiresSession(t *testing.T) {
	lister := &oauthBindingListerStub{byUser: map[int64][]userauth.SocialIdentityLink{}}
	router := chi.NewRouter()
	router.Route("/v1/users/me/oauth-bindings", func(r chi.Router) {
		MountOAuthBindingsRoutes(r, OAuthBindingsDeps{Bindings: lister})
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/users/me/oauth-bindings", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401 body=%s", rec.Code, rec.Body.String())
	}
	if lister.calls != 0 {
		t.Fatalf("list calls=%d want 0 without session", lister.calls)
	}
}
