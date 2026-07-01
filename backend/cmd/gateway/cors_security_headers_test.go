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
	securityHeaders(okHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil))

	want := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s: want %q got %q", k, v, got)
		}
	}
}

// SPA「文档面」(非 API 路径,如 /login、/oauth/callback)必须拿到能加载自身脚本/样式的 CSP,
// 否则 default-src 'none' 会连内嵌前端自己的 JS/CSS 一起拦死 → 整个页面白屏。
// 变异:若 securityHeaders 对所有路径恒设 default-src 'none'(退回分流前),script-src 'self'
// 断言与"不得含 default-src 'none'"断言即 RED —— 这正是活栈测抓到的白屏 bug。
func TestSecurityHeaders_SPADocumentGetsLoadableCSP(t *testing.T) {
	for _, path := range []string{"/login", "/oauth/callback", "/"} {
		rec := httptest.NewRecorder()
		securityHeaders(okHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		csp := rec.Header().Get("Content-Security-Policy")
		if strings.Contains(csp, "default-src 'none'") {
			t.Fatalf("%s 是 SPA 文档,不得用 default-src 'none'(会白屏);got %q", path, csp)
		}
		if !strings.Contains(csp, "script-src 'self'") {
			t.Errorf("%s 的 CSP 必须允许 script-src 'self' 以自举;got %q", path, csp)
		}
		if !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Errorf("%s 的 CSP 仍须保留 frame-ancestors 'none' 防点击劫持;got %q", path, csp)
		}
	}
	// 对照:API 路径必须仍是彻底锁死的严格 CSP(分流不能把 API 也放松)。
	rec := httptest.NewRecorder()
	securityHeaders(okHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil))
	if got := rec.Header().Get("Content-Security-Policy"); got != "default-src 'none'; frame-ancestors 'none'" {
		t.Errorf("API 路径 CSP 必须保持严格 default-src 'none';got %q", got)
	}
}

// HSTS 仅在 TLS 边缘下发——明文场景下绝不断言（否则属于配置错误）。
func TestSecurityHeaders_HSTS_OnlyOnTLSEdge(t *testing.T) {
	// 明文：不下发 HSTS
	rec := httptest.NewRecorder()
	securityHeaders(okHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil))
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS must NOT be set on plaintext; got %q", got)
	}
	// X-Forwarded-Proto: https -> 带有 HSTS
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec2 := httptest.NewRecorder()
	securityHeaders(okHandler()).ServeHTTP(rec2, req)
	if got := rec2.Header().Get("Strict-Transport-Security"); got == "" {
		t.Error("HSTS must be set when X-Forwarded-Proto=https")
	}
}

// 在白名单内的 origin 会被原样回显（绝不会是 "*"），并带上 Vary: Origin。
// 变异：回显 "*" 或跳过白名单校验，断言就会变红。
func TestCORS_AllowedOrigin_EchoedNotWildcard(t *testing.T) {
	allowed := parseAllowedOrigins("https://panel.hkai.shop, https://admin.hkai.shop")
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Origin", "https://panel.hkai.shop")
	rec := httptest.NewRecorder()
	corsMiddleware(allowed)(okHandler()).ServeHTTP(rec, req)

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

// 不允许的 origin 得不到任何 CORS 响应头——浏览器随之会阻止读取。
// 变异：去掉白名单闸门，ACAO 就会变成非空 -> 红。
func TestCORS_DisallowedOrigin_NoHeaders(t *testing.T) {
	allowed := parseAllowedOrigins("https://panel.hkai.shop")
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	corsMiddleware(allowed)(okHandler()).ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("disallowed origin must get NO ACAO; got %q", got)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("non-preflight still passes through (auth handles it); got %d", rec.Code)
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
	rec := httptest.NewRecorder()
	corsMiddleware(allowed)(okHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("allowed preflight must be 204; got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("preflight must advertise allowed methods")
	}
}

// 来自不允许 origin 的预检请求 -> 403，无 CORS 响应头，且永不抵达 handler。
func TestCORS_Preflight_Disallowed_Forbidden(t *testing.T) {
	allowed := parseAllowedOrigins("https://panel.hkai.shop")
	req := httptest.NewRequest(http.MethodOptions, "/v1/api-keys", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	corsMiddleware(allowed)(okHandler()).ServeHTTP(rec, req)

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
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("Origin", "https://panel.hkai.shop")
	rec := httptest.NewRecorder()
	corsMiddleware(allowed)(okHandler()).ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("empty allowlist must grant no cross-origin access")
	}
}
