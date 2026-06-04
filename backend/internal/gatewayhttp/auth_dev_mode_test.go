package gatewayhttp

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

func TestAuthRegisterDevModeReturnsVerificationToken(t *testing.T) {
	t.Setenv("HUAKAI_DEV_AUTH_RETURN_TOKEN", "true")
	now := time.Date(2026, 5, 17, 11, 0, 0, 0, time.UTC)
	authStore := newGatewayMemoryAuthStore(now)
	authSvc := userauth.NewService(authStore)
	authSvc.PasswordPolicy = userauth.PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	authSvc.Now = func() time.Time { return now }
	email := &captureAuthEmail{}
	r := chi.NewRouter()
	r.Route("/v1/auth", func(r chi.Router) {
		MountAuthRoutes(r, AuthHandlerDeps{Auth: authSvc, EmailSender: email})
	})

	rec := serveJSON(t, r, http.MethodPost, "/v1/auth/register", map[string]any{
		"tenant_id": 1, "email": "dev@example.test", "password": "secret",
	})
	assertHTTPStatus(t, rec, http.StatusCreated)
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["verification_token"] == "" || resp["verification_token"] != email.verification {
		t.Fatalf("dev verification token mismatch: resp=%v sent=%q", resp["verification_token"], email.verification)
	}
}

func TestAuthPasswordResetDevModeReturnsResetToken(t *testing.T) {
	t.Setenv("HUAKAI_DEV_AUTH_RETURN_TOKEN", "true")
	now := time.Date(2026, 5, 17, 11, 0, 0, 0, time.UTC)
	authStore := newGatewayMemoryAuthStore(now)
	authSvc := userauth.NewService(authStore)
	authSvc.PasswordPolicy = userauth.PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	authSvc.Now = func() time.Time { return now }
	registered, err := authSvc.Register(t.Context(), userauth.RegisterInput{TenantID: 1, Email: "reset@example.test", Password: "secret"})
	if err != nil || registered.User.ID == 0 {
		t.Fatalf("Register: user=%+v err=%v", registered.User, err)
	}
	email := &captureAuthEmail{}
	r := chi.NewRouter()
	r.Route("/v1/auth", func(r chi.Router) {
		MountAuthRoutes(r, AuthHandlerDeps{Auth: authSvc, EmailSender: email})
	})

	rec := serveJSON(t, r, http.MethodPost, "/v1/auth/reset-password", map[string]any{
		"tenant_id": 1, "email": "reset@example.test",
	})
	assertHTTPStatus(t, rec, http.StatusAccepted)
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["reset_token"] == "" || resp["reset_token"] != email.reset {
		t.Fatalf("dev reset token mismatch: resp=%v sent=%q", resp["reset_token"], email.reset)
	}
}

// TestAuthRegisterDevTokenSuppressedInProduction guards defense-in-depth: even if the dev
// echo flag is mistakenly left on, a production release mode must NOT leak the one-time verification
// secret into the public register response (the startup gate is authoritative; this is the in-handler
// backstop for a runtime-flipped env).
//
// Mutation check: remove the production short-circuit in devAuthReturnTokenEnabled and the response
// regains verification_token under production → this assertion goes red. Discriminating vs the dev
// test above: identical flag, only HUAKAI_RELEASE_MODE differs, and the expected body differs (key
// absent in prod vs present in dev).
func TestAuthRegisterDevTokenSuppressedInProduction(t *testing.T) {
	t.Setenv("HUAKAI_DEV_AUTH_RETURN_TOKEN", "true")
	t.Setenv("HUAKAI_RELEASE_MODE", "production")
	now := time.Date(2026, 5, 17, 11, 0, 0, 0, time.UTC)
	authStore := newGatewayMemoryAuthStore(now)
	authSvc := userauth.NewService(authStore)
	authSvc.PasswordPolicy = userauth.PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	authSvc.Now = func() time.Time { return now }
	email := &captureAuthEmail{}
	r := chi.NewRouter()
	r.Route("/v1/auth", func(r chi.Router) {
		MountAuthRoutes(r, AuthHandlerDeps{Auth: authSvc, EmailSender: email})
	})

	rec := serveJSON(t, r, http.MethodPost, "/v1/auth/register", map[string]any{
		"tenant_id": 1, "email": "prod@example.test", "password": "secret",
	})
	assertHTTPStatus(t, rec, http.StatusCreated)
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, present := resp["verification_token"]; present {
		t.Fatalf("production must NOT echo verification_token even with dev flag on; body=%v", resp)
	}
}
