package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSiteConfigRouteIsAnonymous proves GET /v1/site/config is mounted and
// reachable WITHOUT a session cookie. buildTestRouter wires nil
// platformSettings, so a correctly-mounted anonymous handler answers 503
// (gateway_not_configured) — never 404 (not mounted) and never 401/redirect
// (session required).
//
// Mutation guard: wrap the route in auth.SessionMiddleware and an anonymous
// request returns 401, flipping this assertion red. Removing the mount makes
// it 404, also red.
func TestSiteConfigRouteIsAnonymous(t *testing.T) {
	r := buildTestRouter(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/site/config", nil) // no cookie, no auth header
	r.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("GET /v1/site/config returned 404; anonymous site bootstrap route must be mounted")
	}
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("GET /v1/site/config returned 401; bootstrap endpoint must be anonymous (no session)")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /v1/site/config status=%d body=%s; want 503 under nil-deps test router", rec.Code, rec.Body.String())
	}
}
