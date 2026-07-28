// 全局 CORS 与浏览器安全响应头契约。
//
// 鉴别意图（每个用例逐一标注变异检查）：此前网关没有任何安全响应头、也没有 CORS 策略；
// 一旦某个响应头被丢掉、被回显了不允许的 origin、或重新引入「wildcard 配合 credentials」
// 这类反模式，这些测试就会变红。
package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/browsersession"
	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

// 普通响应上必须带有安全响应头。变异：删除 securityHeaders 中任意一个
// h.Set(...)，对应的断言就会变红。
func TestSecurityHeaders_Present(t *testing.T) {
	rec := httptest.NewRecorder()
	securityHeaders(nil)(okHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil))

	want := map[string]string{
		"X-Content-Type-Options":            "nosniff",
		"X-Frame-Options":                   "DENY",
		"X-Permitted-Cross-Domain-Policies": "none",
		"Cross-Origin-Opener-Policy":        "same-origin",
		"Referrer-Policy":                   "no-referrer",
		"Permissions-Policy":                "camera=(), geolocation=(), payment=(), serial=(), usb=(), microphone=(self)",
		"Content-Security-Policy":           "default-src 'none'; frame-ancestors 'none'",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s: want %q got %q", k, v, got)
		}
	}
}

// 当前进程只提供 API，未命中路由也不得获得可自举脚本或样式的文档 CSP。
// 变异：恢复页面专用 CSP 分支后，非 API 路径会包含 script-src 'self'，本测试转红。
func TestSecurityHeaders_APIOnlyPathsRemainLockedDown(t *testing.T) {
	for _, path := range []string{"/login", "/oauth/callback", "/"} {
		rec := httptest.NewRecorder()
		securityHeaders(nil)(okHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if got := rec.Header().Get("Content-Security-Policy"); got != "default-src 'none'; frame-ancestors 'none'" {
			t.Errorf("%s 的 CSP=%q，期望 API-only 严格策略", path, got)
		}
	}
}

// HSTS 仅在 TLS 边缘下发——明文场景下绝不断言（否则属于配置错误）。
func TestSecurityHeaders_HSTS_OnlyOnTLSEdge(t *testing.T) {
	// 明文：不下发 HSTS
	rec := httptest.NewRecorder()
	securityHeaders(nil)(okHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil))
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS must NOT be set on plaintext; got %q", got)
	}
	// X-Forwarded-Proto: https -> 带有 HSTS
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec2 := httptest.NewRecorder()
	resolver, err := clientip.NewResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	req.RemoteAddr = "10.1.2.3:5000"
	securityHeaders(resolver)(okHandler()).ServeHTTP(rec2, req)
	if got := rec2.Header().Get("Strict-Transport-Security"); got == "" {
		t.Error("HSTS must be set when X-Forwarded-Proto=https")
	}
	// 非可信 socket 对端即使伪造同名头也不能被当成 TLS 边缘。
	spoof := httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil)
	spoof.RemoteAddr = "203.0.113.7:5000"
	spoof.Header.Set("X-Forwarded-Proto", "https")
	spoofRec := httptest.NewRecorder()
	securityHeaders(resolver)(okHandler()).ServeHTTP(spoofRec, spoof)
	if got := spoofRec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("非可信对端伪造 X-Forwarded-Proto 不得触发 HSTS；got %q", got)
	}
}

// 在白名单内的 origin 会被原样回显（绝不会是 "*"），并带上 Vary: Origin。
// 变异：回显 "*" 或跳过白名单校验，断言就会变红。
func TestCORS_AllowedOrigin_EchoedNotWildcard(t *testing.T) {
	allowed := parseAllowedOrigins("https://panel.hkai.shop, https://admin.hkai.shop")
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Origin", "https://panel.hkai.shop")
	rec := httptest.NewRecorder()
	corsMiddleware(allowed, nil)(okHandler()).ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://panel.hkai.shop" {
		t.Errorf("ACAO must echo the allowlisted origin, not %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got == "*" {
		t.Error("ACAO must never be wildcard")
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Error("ACAC should be true for an allowlisted origin")
	}
	if rec.Header().Get("Vary") == "" {
		t.Error("Vary: Origin must be set so caches don't cross origins")
	}
}

// Cookie 浏览器会话必须保持同源，即便来源进入普通 CORS 白名单也不能放行。
// 变异：删除 browsersession.IsBrowser 分支后，handler 会被调用并返回 200。
func TestCORS_AllowlistedCrossOriginBrowserSessionRejected(t *testing.T) {
	const origin = "https://panel.hkai.shop"
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodPost, "https://api.hkai.shop/v1/auth/login", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set(browsersession.ModeHeader, browsersession.ModeBrowser)
	rec := httptest.NewRecorder()
	corsMiddleware(parseAllowedOrigins(origin), nil)(handler).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden || called {
		t.Fatalf("跨源 Cookie 会话必须在 handler 前拒绝：status=%d called=%v", rec.Code, called)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("可信页面必须能读取稳定错误：want origin %q got %q", origin, got)
	}
	if !strings.Contains(rec.Body.String(), `"code":"browser_session_cross_origin_forbidden"`) {
		t.Fatalf("缺少稳定错误码：%s", rec.Body.String())
	}
}

// 同源 Cookie 浏览器会话无需配置 CORS 白名单，必须继续进入业务 handler。
func TestCORS_SameOriginBrowserSessionAllowed(t *testing.T) {
	const origin = "https://api.hkai.shop"
	req := httptest.NewRequest(http.MethodPost, origin+"/v1/auth/login", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set(browsersession.ModeHeader, browsersession.ModeBrowser)
	rec := httptest.NewRecorder()
	corsMiddleware(parseAllowedOrigins(""), nil)(okHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("同源 Cookie 会话应放行：status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// 不允许的 origin 必须在 handler 前被 403；只省略 CORS 响应头仍可能让带
// Cookie 的写副作用发生。变异：恢复“普通请求继续透传”，状态和 handler 计数都会变红。
func TestCORS_DisallowedOrigin_ForbiddenBeforeHandler(t *testing.T) {
	allowed := parseAllowedOrigins("https://panel.hkai.shop")
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodPost, "https://api.hkai.shop/v1/sessions", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	corsMiddleware(allowed, nil)(handler).ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("disallowed origin must get NO ACAO; got %q", got)
	}
	if rec.Code != http.StatusForbidden || called {
		t.Fatalf("不可信来源必须在 handler 前拒绝：status=%d called=%v", rec.Code, called)
	}
	// 缓存安全：即便没有任何 CORS 响应头，响应也必须带 Vary: Origin，
	// 这样共享缓存才不会把它复用给白名单内的 origin。变异：去掉 corsMiddleware 中
	// 那条无条件的 Vary，本断言就会变红。
	if rec.Header().Get("Vary") != "Origin" {
		t.Errorf("disallowed/origin-dependent response must carry Vary: Origin; got %q", rec.Header().Get("Vary"))
	}
}

// 来自白名单内 origin 的预检请求 -> 204 + 方法/响应头契约。
func TestCORS_Preflight_Allowed(t *testing.T) {
	allowed := parseAllowedOrigins("https://panel.hkai.shop")
	req := httptest.NewRequest(http.MethodOptions, "/v1/api-keys", nil)
	req.Header.Set("Origin", "https://panel.hkai.shop")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization, x-huakai-csrf, idempotency-key")
	rec := httptest.NewRecorder()
	corsMiddleware(allowed, nil)(okHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("allowed preflight must be 204; got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("preflight must advertise allowed methods")
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != corsAllowHeaders {
		t.Fatalf("预检必须返回固定请求头合同，不得回显调用方输入：got %q", got)
	}
}

// 官方 OpenAI/Anthropic 浏览器 SDK 会自动发送这些有限的 Stainless 元数据头。
// 变异：从 corsAllowedRequestHeaders 删除任意一项后，这个真实预检形状会返回 403。
func TestCORS_Preflight_OfficialBrowserSDKHeadersAllowed(t *testing.T) {
	headers := []string{
		"Anthropic-Dangerous-Direct-Browser-Access",
		"Authorization",
		"Content-Type",
		"X-Stainless-Arch",
		"X-Stainless-Custom-Poll-Interval",
		"X-Stainless-Helper",
		"X-Stainless-Helper-Method",
		"X-Stainless-Lang",
		"X-Stainless-OS",
		"X-Stainless-Package-Version",
		"X-Stainless-Poll-Helper",
		"X-Stainless-Retry-Count",
		"X-Stainless-Runtime",
		"X-Stainless-Runtime-Version",
		"X-Stainless-Timeout",
	}
	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "https://panel.hkai.shop")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", strings.Join(headers, ", "))
	rec := httptest.NewRecorder()
	corsMiddleware(parseAllowedOrigins("https://panel.hkai.shop"), nil)(okHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("官方浏览器 SDK 预检应通过：status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCORS_Preflight_UnknownHeaderOrMethodForbidden(t *testing.T) {
	allowed := parseAllowedOrigins("https://panel.hkai.shop")
	for _, tc := range []struct {
		name    string
		method  string
		headers string
	}{
		{name: "未知请求头", method: "POST", headers: "Authorization, X-Attacker-Controlled"},
		{name: "未知 Stainless 请求头", method: "POST", headers: "Authorization, X-Stainless-Private"},
		{name: "未知方法", method: "TRACE", headers: "Authorization"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodOptions, "/v1/api-keys", nil)
			req.Header.Set("Origin", "https://panel.hkai.shop")
			req.Header.Set("Access-Control-Request-Method", tc.method)
			req.Header.Set("Access-Control-Request-Headers", tc.headers)
			rec := httptest.NewRecorder()
			corsMiddleware(allowed, nil)(okHandler()).ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status=%d want 403", rec.Code)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Fatalf("拒绝响应不得保留授权来源头：%q", got)
			}
		})
	}
}

// 来自不允许 origin 的预检请求 -> 403，无 CORS 响应头，且永不抵达 handler。
func TestCORS_Preflight_Disallowed_Forbidden(t *testing.T) {
	allowed := parseAllowedOrigins("https://panel.hkai.shop")
	req := httptest.NewRequest(http.MethodOptions, "/v1/api-keys", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	corsMiddleware(allowed, nil)(okHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("disallowed preflight must be 403; got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("disallowed preflight must not leak ACAO")
	}
}

// 空白名单 = 默认拒绝：即便是格式完全正确的 origin 也什么都拿不到。
func TestCORS_EmptyAllowlist_DefaultDeny(t *testing.T) {
	allowed := parseAllowedOrigins("")
	req := httptest.NewRequest(http.MethodGet, "https://api.hkai.shop/v1/sessions", nil)
	req.Header.Set("Origin", "https://panel.hkai.shop")
	rec := httptest.NewRecorder()
	corsMiddleware(allowed, nil)(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("空白名单必须拒绝跨源：status=%d origin=%q", rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORS_SameOriginWorksWithoutConfiguredAllowlist(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://panel.hkai.shop/v1/sessions", nil)
	req.Header.Set("Origin", "https://panel.hkai.shop")
	rec := httptest.NewRecorder()
	corsMiddleware(parseAllowedOrigins(""), nil)(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("同源请求不应依赖跨源白名单：status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://panel.hkai.shop" {
		t.Fatalf("origin=%q want same origin", got)
	}
}

func TestCORS_SameHostDifferentSchemeIsNotSameOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://panel.hkai.shop/v1/sessions", nil)
	req.Header.Set("Origin", "https://panel.hkai.shop")
	rec := httptest.NewRecorder()
	corsMiddleware(parseAllowedOrigins(""), nil)(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("同主机但不同协议必须按跨源拒绝：status=%d", rec.Code)
	}
}

func TestCORS_TrustedTLSProxyPreservesSameOrigin(t *testing.T) {
	resolver, err := clientip.NewResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://panel.hkai.shop/v1/sessions", nil)
	req.RemoteAddr = "10.1.2.3:5000"
	req.Header.Set("Origin", "https://panel.hkai.shop")
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	corsMiddleware(parseAllowedOrigins(""), resolver)(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("可信 TLS 终止代理后的同源请求必须通过：status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://panel.hkai.shop" {
		t.Fatalf("origin=%q want https://panel.hkai.shop", got)
	}
}

func TestCORS_UntrustedOrAmbiguousForwardedProtoCannotForgeSameOrigin(t *testing.T) {
	resolver, err := clientip.NewResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name       string
		remoteAddr string
		addHeaders []string
	}{
		{name: "公网对端伪造", remoteAddr: "203.0.113.7:5000", addHeaders: []string{"https"}},
		{name: "可信代理逗号歧义", remoteAddr: "10.1.2.3:5000", addHeaders: []string{"https,http"}},
		{name: "可信代理重复字段", remoteAddr: "10.1.2.3:5000", addHeaders: []string{"https", "https"}},
		{name: "可信代理非法协议", remoteAddr: "10.1.2.3:5000", addHeaders: []string{"javascript"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://panel.hkai.shop/v1/sessions", nil)
			req.RemoteAddr = tc.remoteAddr
			req.Header.Set("Origin", "https://panel.hkai.shop")
			for _, value := range tc.addHeaders {
				req.Header.Add("X-Forwarded-Proto", value)
			}
			rec := httptest.NewRecorder()
			corsMiddleware(parseAllowedOrigins(""), resolver)(okHandler()).ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("不可信或歧义协议不得伪造成 HTTPS 同源：status=%d", rec.Code)
			}
		})
	}
}

func TestParseAllowedOrigins_RejectsInsecureRemoteAndMalformedValues(t *testing.T) {
	allowed := parseAllowedOrigins(strings.Join([]string{
		"https://PANEL.hkai.shop/",
		"http://127.0.0.1:5173",
		"http://panel.hkai.shop",
		"https://panel.hkai.shop/path",
		"https://user:pass@panel.hkai.shop",
		"null",
	}, ","))
	for _, want := range []string{"https://panel.hkai.shop", "http://127.0.0.1:5173"} {
		if _, ok := allowed[want]; !ok {
			t.Errorf("缺少合法来源 %q；got %#v", want, allowed)
		}
	}
	if len(allowed) != 2 {
		t.Fatalf("不安全或畸形来源不应进入白名单；got %#v", allowed)
	}
}

func TestCORS_NoOriginServerClientPassesThrough(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://api.hkai.shop/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	corsMiddleware(parseAllowedOrigins(""), nil)(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("无 Origin 的 CLI/SDK 请求必须保持兼容：status=%d", rec.Code)
	}
}
