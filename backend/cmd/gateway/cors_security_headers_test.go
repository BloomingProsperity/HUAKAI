// S2-141: global CORS + browser security-header contract.
//
// Discriminating intent (mutation checks called out per case): the gateway had
// ZERO security headers and no CORS policy; these tests go red if a header is
// dropped, if a disallowed origin is echoed, or if the wildcard-with-credentials
// anti-pattern is reintroduced.
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

// Security headers must be present on a normal response. Mutation: delete any
// h.Set(...) in securityHeaders and the matching assertion goes red.
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

// HSTS only over TLS edge — never asserted on plaintext (would be a misconfig).
func TestSecurityHeaders_HSTS_OnlyOnTLSEdge(t *testing.T) {
	// plaintext: no HSTS
	rec := httptest.NewRecorder()
	securityHeaders(okHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil))
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS must NOT be set on plaintext; got %q", got)
	}
	// X-Forwarded-Proto: https -> HSTS present
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec2 := httptest.NewRecorder()
	securityHeaders(okHandler()).ServeHTTP(rec2, req)
	if got := rec2.Header().Get("Strict-Transport-Security"); got == "" {
		t.Error("HSTS must be set when X-Forwarded-Proto=https")
	}
}

// Allowlisted origin is echoed back exactly (never "*"), with Vary: Origin.
// Mutation: echo "*" or skip the allowlist check and the assertions go red.
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

// Disallowed origin gets NO CORS headers — the browser then blocks the read.
// Mutation: drop the allowlist gate and ACAO becomes non-empty -> red.
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
	// Cache safety: even with no CORS headers, the response must Vary: Origin so a
	// shared cache can't reuse it for an allowlisted origin. Mutation: drop the
	// unconditional Vary in corsMiddleware and this goes red.
	if rec.Header().Get("Vary") != "Origin" {
		t.Errorf("disallowed/origin-dependent response must carry Vary: Origin; got %q", rec.Header().Get("Vary"))
	}
}

// Preflight from an allowlisted origin -> 204 + method/header contract.
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

// Preflight from a disallowed origin -> 403, no CORS headers, never reaches handler.
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

// Empty allowlist = default-deny: even a well-formed origin gets nothing.
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
