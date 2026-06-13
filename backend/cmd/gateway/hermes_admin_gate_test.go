package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
)

// newHermesGateTestDeps builds a deps with both hermesService and hermesRunner
// non-nil (so the /v1/hermes mount condition is satisfied) plus an
// admin.AdminResolver whose queries are nil. A nil-queries resolver returns
// ErrAdminBackend from Resolve, which the admin middleware maps to 503 with a
// distinct code — letting an in-process test discriminate the admin mount path
// from the legacy path WITHOUT a database.
func newHermesGateTestDeps(t *testing.T, adminOnly bool) *deps {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	runner, err := hermes.NewRunnerClient(hermes.RunnerConfig{
		RunnerURL:     "http://runner.local",
		JWTPrivateKey: privateKey,
		JWTKID:        "kid-test",
		HTTPClient:    &http.Client{},
	})
	if err != nil {
		t.Fatalf("NewRunnerClient: %v", err)
	}
	return &deps{
		cfg:             &config.Config{BillingPolicyVersion: "test-1.0", RequestClass: "standard"},
		hermesService:   hermes.NewService(&hermesAuditStoreSpy{}),
		hermesRunner:    runner,
		hermesAdminOnly: adminOnly,
		// nil queries -> Resolve returns ErrAdminBackend (fail-closed 503).
		adminAuth: admin.NewAdminResolver(nil),
		// inboundAuth is intentionally nil: the legacy APIKeyMiddleware maps a nil
		// resolver to 503 hermes_auth_unavailable, distinct from the admin codes.
	}
}

func TestHermesAdminOnlyModeUsesAdminGate(t *testing.T) {
	// Regression (mutation: revert routes.go to always use APIKeyMiddleware): in
	// admin-only mode a no-credential request to a Hermes endpoint is handled by
	// the ADMIN middleware. With a nil-queries admin resolver it fails closed as
	// 503 hermes_admin_backend_error — a code the legacy path never emits — which
	// proves the admin middleware (not the customer-key middleware) is mounted.
	d := newHermesGateTestDeps(t, true)
	r := chi.NewRouter()
	mountRoutes(r, d, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/v1/hermes/conversations", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hermes_admin_backend_error") {
		t.Fatalf("body=%s want hermes_admin_backend_error (admin middleware mounted)", rec.Body.String())
	}
}

func TestHermesAdminOnlyFalsePreservesLegacyEndUserPath(t *testing.T) {
	// Regression (rollback path): with HUAKAI_HERMES_ADMIN_ONLY=false the legacy
	// customer-key APIKeyMiddleware is mounted verbatim. A no-credential request
	// is handled by THAT middleware — its customer-key resolver path yields the
	// legacy hermes_auth_backend_error code, which is distinct from the admin
	// middleware's hermes_admin_backend_error, proving the rollback path is
	// intact. (Mutation: if routes.go ignored the flag and always mounted the
	// admin middleware, this body would carry the admin code instead.)
	d := newHermesGateTestDeps(t, false)
	r := chi.NewRouter()
	mountRoutes(r, d, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/v1/hermes/conversations", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s want 503", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "hermes_auth_backend_error") {
		t.Fatalf("body=%s want hermes_auth_backend_error (legacy middleware mounted)", body)
	}
	if strings.Contains(body, "hermes_admin_") {
		t.Fatalf("body=%s unexpectedly carries an admin-path code in legacy mode", body)
	}
}

func TestHermesAdminOnlyFromEnvDefaultsTrue(t *testing.T) {
	// Regression: the flag must default to admin-only (true) when unset, and
	// reject a malformed value rather than silently defaulting. Mutation:
	// flipping the default to false would silently re-expose Hermes to end users.
	t.Setenv(hermesAdminOnlyEnv, "")
	if v, err := hermesAdminOnlyFromEnv(); err != nil || !v {
		t.Fatalf("default v=%v err=%v want true,nil", v, err)
	}
	t.Setenv(hermesAdminOnlyEnv, "false")
	if v, err := hermesAdminOnlyFromEnv(); err != nil || v {
		t.Fatalf("false v=%v err=%v want false,nil", v, err)
	}
	t.Setenv(hermesAdminOnlyEnv, "not-a-bool")
	if _, err := hermesAdminOnlyFromEnv(); err == nil {
		t.Fatalf("malformed value must be a boot error, got nil")
	}
}
