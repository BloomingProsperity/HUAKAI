package browsersession

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

func TestDeliverBrowserKeepsRefreshTokenOutOfResponseAndHardensCookies(t *testing.T) {
	expires := time.Now().Add(24 * time.Hour).UTC()
	issued := usersession.IssuedTokens{
		SessionToken:  "short-lived",
		RefreshToken:  "long-lived-secret",
		RefreshExpiry: expires,
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	req.Header.Set(ModeHeader, ModeBrowser)
	rec := httptest.NewRecorder()

	got := Deliver(rec, req, issued)

	if got.SessionToken != issued.SessionToken || got.RefreshToken != "" || got.CSRFToken == "" {
		t.Fatalf("浏览器交付结果不符合短令牌/CSRF 合同: %+v", got)
	}
	if strings.Contains(got.CSRFToken, issued.RefreshToken) {
		t.Fatal("CSRF 证明不得包含刷新令牌原文")
	}
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Pragma") != "no-cache" {
		t.Fatalf("令牌响应缺少禁止缓存头: %v", rec.Header())
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("Cookie 数量=%d，期望 2", len(cookies))
	}
	var refresh, csrf *http.Cookie
	for _, cookie := range cookies {
		switch cookie.Name {
		case RefreshCookieName:
			refresh = cookie
		case CSRFCookieName:
			csrf = cookie
		}
	}
	if refresh == nil || refresh.Value != issued.RefreshToken || !refresh.HttpOnly ||
		!refresh.Secure || refresh.Path != "/" || refresh.SameSite != http.SameSiteStrictMode {
		t.Fatalf("刷新 Cookie 安全属性错误: %+v", refresh)
	}
	if csrf == nil || csrf.Value != got.CSRFToken || csrf.HttpOnly ||
		!csrf.Secure || csrf.Path != "/" || csrf.SameSite != http.SameSiteStrictMode {
		t.Fatalf("CSRF Cookie 安全属性错误: %+v", csrf)
	}
}

func TestDeliverAPIKeepsExistingJSONContractAndSetsNoCookies(t *testing.T) {
	issued := usersession.IssuedTokens{SessionToken: "session", RefreshToken: "refresh"}
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	rec := httptest.NewRecorder()

	got := Deliver(rec, req, issued)

	if got.RefreshToken != issued.RefreshToken || got.CSRFToken != "" {
		t.Fatalf("API 交付合同被改变: %+v", got)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatalf("API 模式不应写 Cookie: %v", rec.Result().Cookies())
	}
}

func TestResolveRefreshBrowserRequiresBoundCookieAndHeader(t *testing.T) {
	issued := usersession.IssuedTokens{
		RefreshToken:  "rotating-refresh",
		RefreshExpiry: time.Now().Add(time.Hour),
	}
	loginReq := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	loginReq.Header.Set(ModeHeader, ModeBrowser)
	loginRec := httptest.NewRecorder()
	view := Deliver(loginRec, loginReq, issued)

	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/refresh", nil)
	req.Header.Set(ModeHeader, ModeBrowser)
	req.Header.Set(CSRFHeader, view.CSRFToken)
	for _, cookie := range loginRec.Result().Cookies() {
		req.AddCookie(cookie)
	}

	token, browser, err := ResolveRefresh(req, "")
	if err != nil || !browser || token != issued.RefreshToken {
		t.Fatalf("浏览器刷新解析结果 token=%q browser=%v err=%v", token, browser, err)
	}

	req.Header.Set(CSRFHeader, "forged")
	if _, _, err := ResolveRefresh(req, ""); !errors.Is(err, ErrCSRFInvalid) {
		t.Fatalf("伪造 CSRF 应被拒绝，得到 %v", err)
	}
	req.Header.Set(CSRFHeader, view.CSRFToken)
	if _, _, err := ResolveRefresh(req, "body-secret"); !errors.Is(err, ErrRefreshBodyConflict) {
		t.Fatalf("浏览器模式请求体刷新令牌应被拒绝，得到 %v", err)
	}
}

func TestClearExpiresBothBrowserCookies(t *testing.T) {
	rec := httptest.NewRecorder()
	Clear(rec)
	cookies := rec.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("清理 Cookie 数量=%d，期望 2", len(cookies))
	}
	for _, cookie := range cookies {
		if cookie.MaxAge != -1 || cookie.Value != "" || !cookie.Secure ||
			cookie.Path != "/" || cookie.SameSite != http.SameSiteStrictMode {
			t.Fatalf("Cookie 未被安全清理: %+v", cookie)
		}
	}
}
