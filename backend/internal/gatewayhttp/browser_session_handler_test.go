package gatewayhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/browsersession"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

type failingBrowserRefreshStore struct {
	usersession.Store
	err error
}

func (s *failingBrowserRefreshStore) RotateSession(
	context.Context,
	usersession.SessionFamily,
	usersession.RefreshToken,
	usersession.RefreshToken,
	usersession.SessionToken,
	time.Time,
) (usersession.SessionFamily, error) {
	return usersession.SessionFamily{}, s.err
}

func TestBrowserSessionLoginRefreshAndReplayClosure(t *testing.T) {
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	authStore := newGatewayMemoryAuthStore(now)
	seedLoginUser(t, authStore, "browser@example.test", "secret12", userauth.UserStatusActive, true)
	authSvc := userauth.NewService(authStore)
	authSvc.PasswordPolicy = userauth.PasswordPolicy{
		MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16,
	}
	authSvc.Now = func() time.Time { return now }
	sessionSvc := usersession.NewService(usersession.NewMemoryStore())
	sessionSvc.Now = func() time.Time { return now }
	sessionSvc.SigningKey = testSessionSigningKey()

	router := chi.NewRouter()
	router.Route("/v1/auth", func(r chi.Router) {
		MountAuthRoutes(r, AuthHandlerDeps{Auth: authSvc, Sessions: sessionSvc})
	})
	router.Route("/v1/sessions", func(r chi.Router) {
		MountSessionRefreshRoute(r, SessionHandlerDeps{Sessions: sessionSvc})
	})

	login := serveBrowserSessionJSON(t, router, "/v1/auth/login", map[string]any{
		"tenant_id": 1,
		"email":     "browser@example.test",
		"password":  "secret12",
	}, nil, "")
	assertHTTPStatus(t, login, http.StatusOK)
	loginView := decodeBrowserSessionView(t, login)
	if loginView.SessionToken == "" || loginView.RefreshToken != "" || loginView.CSRFToken == "" {
		t.Fatalf("浏览器登录泄露长期令牌或缺少短期令牌/CSRF: %+v", loginView)
	}
	if strings.Contains(login.Body.String(), "long-lived") ||
		strings.Contains(login.Body.String(), `"refresh_token"`) {
		t.Fatalf("浏览器登录响应不应出现 refresh_token 字段: %s", login.Body.String())
	}
	loginCookies := sessionCookies(t, login)

	now = now.Add(time.Minute)
	refresh := serveBrowserSessionJSON(t, router, "/v1/sessions/refresh", map[string]any{},
		loginCookies, loginView.CSRFToken)
	assertHTTPStatus(t, refresh, http.StatusOK)
	refreshView := decodeBrowserSessionView(t, refresh)
	if refreshView.SessionToken == "" || refreshView.SessionToken == loginView.SessionToken ||
		refreshView.RefreshToken != "" || refreshView.CSRFToken == "" ||
		refreshView.CSRFToken == loginView.CSRFToken {
		t.Fatalf("浏览器刷新未轮换短令牌、CSRF 或泄露刷新令牌: %+v", refreshView)
	}

	replay := serveBrowserSessionJSON(t, router, "/v1/sessions/refresh", map[string]any{},
		loginCookies, loginView.CSRFToken)
	assertHTTPStatus(t, replay, http.StatusConflict)
	if code := loginErrorCode(t, replay); code != "refresh_token_replay" {
		t.Fatalf("旧刷新令牌重放错误码=%q，期望 refresh_token_replay", code)
	}
	assertExpiredBrowserCookies(t, replay)
}

func TestBrowserSessionRefreshRejectsCSRFAndBodyTokenAmbiguity(t *testing.T) {
	sessionSvc := usersession.NewService(usersession.NewMemoryStore())
	sessionSvc.SigningKey = testSessionSigningKey()
	issued, err := sessionSvc.Create(t.Context(), usersession.CreateInput{TenantID: 1, UserID: 7})
	if err != nil {
		t.Fatalf("创建会话: %v", err)
	}
	loginReq := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	loginReq.Header.Set(browsersession.ModeHeader, browsersession.ModeBrowser)
	loginRec := httptest.NewRecorder()
	view := browsersession.Deliver(loginRec, loginReq, issued)
	cookies := sessionCookies(t, loginRec)

	router := chi.NewRouter()
	router.Route("/v1/sessions", func(r chi.Router) {
		MountSessionRefreshRoute(r, SessionHandlerDeps{Sessions: sessionSvc})
	})

	missingCSRF := serveBrowserSessionJSON(t, router, "/v1/sessions/refresh", map[string]any{},
		cookies, "")
	assertHTTPStatus(t, missingCSRF, http.StatusForbidden)
	if code := loginErrorCode(t, missingCSRF); code != "browser_csrf_invalid" {
		t.Fatalf("缺少 CSRF 错误码=%q，期望 browser_csrf_invalid", code)
	}

	bodyConflict := serveBrowserSessionJSON(t, router, "/v1/sessions/refresh", map[string]any{
		"refresh_token": issued.RefreshToken,
	}, cookies, view.CSRFToken)
	assertHTTPStatus(t, bodyConflict, http.StatusBadRequest)
	if code := loginErrorCode(t, bodyConflict); code != "browser_refresh_body_forbidden" {
		t.Fatalf("双来源刷新令牌错误码=%q，期望 browser_refresh_body_forbidden", code)
	}
}

func TestBrowserSessionRefreshClearsCookiesAfterAuthenticationVersionChange(t *testing.T) {
	store := &failingBrowserRefreshStore{
		Store: usersession.NewMemoryStore(),
		err:   usersession.ErrAuthenticationStale,
	}
	sessionSvc := usersession.NewService(store)
	sessionSvc.SigningKey = testSessionSigningKey()
	issued, err := sessionSvc.Create(t.Context(), usersession.CreateInput{
		TenantID: 1, UserID: 7, AuthVersion: 1,
	})
	if err != nil {
		t.Fatalf("创建会话: %v", err)
	}
	loginReq := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	loginReq.Header.Set(browsersession.ModeHeader, browsersession.ModeBrowser)
	loginRec := httptest.NewRecorder()
	view := browsersession.Deliver(loginRec, loginReq, issued)

	router := chi.NewRouter()
	router.Route("/v1/sessions", func(r chi.Router) {
		MountSessionRefreshRoute(r, SessionHandlerDeps{Sessions: sessionSvc})
	})
	refresh := serveBrowserSessionJSON(
		t,
		router,
		"/v1/sessions/refresh",
		map[string]any{},
		sessionCookies(t, loginRec),
		view.CSRFToken,
	)

	assertHTTPStatus(t, refresh, http.StatusUnauthorized)
	if code := loginErrorCode(t, refresh); code != "authentication_stale" {
		t.Fatalf("认证版本过期错误码=%q，期望 authentication_stale", code)
	}
	assertExpiredBrowserCookies(t, refresh)
}

func TestBrowserSessionRefreshKeepsCookiesAfterTransientBackendError(t *testing.T) {
	store := &failingBrowserRefreshStore{
		Store: usersession.NewMemoryStore(),
		err:   errors.New("数据库暂时不可用"),
	}
	sessionSvc := usersession.NewService(store)
	sessionSvc.SigningKey = testSessionSigningKey()
	issued, err := sessionSvc.Create(t.Context(), usersession.CreateInput{
		TenantID: 1, UserID: 8, AuthVersion: 1,
	})
	if err != nil {
		t.Fatalf("创建会话: %v", err)
	}
	loginReq := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	loginReq.Header.Set(browsersession.ModeHeader, browsersession.ModeBrowser)
	loginRec := httptest.NewRecorder()
	view := browsersession.Deliver(loginRec, loginReq, issued)

	router := chi.NewRouter()
	router.Route("/v1/sessions", func(r chi.Router) {
		MountSessionRefreshRoute(r, SessionHandlerDeps{Sessions: sessionSvc})
	})
	refresh := serveBrowserSessionJSON(
		t,
		router,
		"/v1/sessions/refresh",
		map[string]any{},
		sessionCookies(t, loginRec),
		view.CSRFToken,
	)

	assertHTTPStatus(t, refresh, http.StatusServiceUnavailable)
	if code := loginErrorCode(t, refresh); code != "session_backend_error" {
		t.Fatalf("瞬时后端错误码=%q，期望 session_backend_error", code)
	}
	for _, cookie := range refresh.Result().Cookies() {
		if cookie.Name == browsersession.RefreshCookieName || cookie.Name == browsersession.CSRFCookieName {
			t.Fatalf("瞬时后端错误不得清理可重试的浏览器 Cookie: %+v", cookie)
		}
	}
}

func serveBrowserSessionJSON(
	t *testing.T,
	handler http.Handler,
	path string,
	body map[string]any,
	cookies []*http.Cookie,
	csrf string,
) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("编码请求: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(browsersession.ModeHeader, browsersession.ModeBrowser)
	if csrf != "" {
		req.Header.Set(browsersession.CSRFHeader, csrf)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeBrowserSessionView(t *testing.T, rec *httptest.ResponseRecorder) browsersession.Tokens {
	t.Helper()
	var response struct {
		Session browsersession.Tokens `json:"session"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("解析会话响应: %v body=%s", err, rec.Body.String())
	}
	return response.Session
}

func sessionCookies(t *testing.T, rec *httptest.ResponseRecorder) []*http.Cookie {
	t.Helper()
	var out []*http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == browsersession.RefreshCookieName || cookie.Name == browsersession.CSRFCookieName {
			out = append(out, cookie)
		}
	}
	if len(out) != 2 {
		t.Fatalf("浏览器会话 Cookie 数量=%d，期望 2；headers=%v", len(out), rec.Header())
	}
	return out
}

func assertExpiredBrowserCookies(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	cookies := sessionCookies(t, rec)
	for _, cookie := range cookies {
		if cookie.MaxAge != -1 || cookie.Value != "" {
			t.Fatalf("终止性刷新错误后 Cookie 未清理: %+v", cookie)
		}
	}
}
