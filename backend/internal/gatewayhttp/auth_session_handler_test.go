package gatewayhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

func TestAuthAndSessionHandlersRegisterVerifyLoginRefreshList(t *testing.T) {
	now := time.Date(2026, 5, 16, 9, 0, 0, 0, time.UTC)
	authStore := newGatewayMemoryAuthStore(now)
	authSvc := userauth.NewService(authStore)
	authSvc.PasswordPolicy = userauth.PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	authSvc.Now = func() time.Time { return now }
	sessionSvc := usersession.NewService(usersession.NewMemoryStore())
	sessionSvc.Now = func() time.Time { return now }
	sessionSvc.SigningKey = testSessionSigningKey()
	email := &captureAuthEmail{}
	r := chi.NewRouter()
	r.Route("/v1/auth", func(r chi.Router) {
		MountAuthRoutes(r, AuthHandlerDeps{Auth: authSvc, Sessions: sessionSvc, EmailSender: email})
	})
	r.Route("/v1/sessions", func(r chi.Router) {
		r.Use(sessionauth.SessionMiddleware(sessionSvc, nil))
		MountSessionRoutes(r, SessionHandlerDeps{Sessions: sessionSvc})
	})

	rec := serveJSON(t, r, http.MethodPost, "/v1/auth/register", map[string]any{
		"tenant_id": 1, "email": "user@example.test", "password": "secret",
	})
	assertHTTPStatus(t, rec, http.StatusCreated)
	if email.verification == "" {
		t.Fatal("verification token was not sent")
	}

	rec = serveJSON(t, r, http.MethodPost, "/v1/auth/verify-email", map[string]any{
		"tenant_id": 1, "token": email.verification,
	})
	assertHTTPStatus(t, rec, http.StatusOK)

	rec = serveJSON(t, r, http.MethodPost, "/v1/auth/login", map[string]any{
		"tenant_id": 1, "email": "user@example.test", "password": "secret",
	})
	assertHTTPStatus(t, rec, http.StatusOK)
	var loginResp struct {
		Session usersession.IssuedTokens `json:"session"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if loginResp.Session.RefreshToken == "" {
		t.Fatal("login did not return refresh token")
	}

	now = now.Add(time.Minute)
	rec = serveJSON(t, r, http.MethodPost, "/v1/sessions/refresh", map[string]any{
		"refresh_token": loginResp.Session.RefreshToken,
	}, loginResp.Session.SessionToken)
	assertHTTPStatus(t, rec, http.StatusOK)
	var refreshResp struct {
		Session usersession.IssuedTokens `json:"session"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &refreshResp); err != nil {
		t.Fatalf("decode refresh response: %v", err)
	}

	rec = serveJSON(t, r, http.MethodPost, "/v1/sessions/list", map[string]any{
		"tenant_id": 999, "user_id": int64(999999),
	}, refreshResp.Session.SessionToken)
	assertHTTPStatus(t, rec, http.StatusOK)
}

func TestAT_SESSION_001_004_HandlersRequireBearerAndIgnoreBodyUser(t *testing.T) {
	now := time.Date(2026, 5, 16, 14, 0, 0, 0, time.UTC)
	sessionSvc := usersession.NewService(usersession.NewMemoryStore())
	sessionSvc.Now = func() time.Time { return now }
	sessionSvc.SigningKey = testSessionSigningKey()
	issued, err := sessionSvc.Create(context.Background(), usersession.CreateInput{
		TenantID: 7, UserID: 7001, IP: "10.1.1.1", UserAgent: "Chrome/1",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	other, err := sessionSvc.Create(context.Background(), usersession.CreateInput{
		TenantID: 7, UserID: 7002, IP: "10.2.1.1", UserAgent: "Firefox/1",
	})
	if err != nil {
		t.Fatalf("Create other: %v", err)
	}
	r := chi.NewRouter()
	r.Route("/v1/sessions", func(r chi.Router) {
		r.Use(sessionauth.SessionMiddleware(sessionSvc, nil))
		MountSessionRoutes(r, SessionHandlerDeps{Sessions: sessionSvc})
	})

	rec := serveJSON(t, r, http.MethodPost, "/v1/sessions/list", map[string]any{"tenant_id": 7, "user_id": 7001})
	assertHTTPStatus(t, rec, http.StatusUnauthorized)

	rec = serveJSON(t, r, http.MethodPost, "/v1/sessions/list", map[string]any{"tenant_id": 999, "user_id": 7002}, issued.SessionToken)
	assertHTTPStatus(t, rec, http.StatusOK)
	var listResp struct {
		Families []usersession.SessionFamily `json:"families"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResp.Families) != 1 || listResp.Families[0].UserID != 7001 {
		t.Fatalf("handler trusted body user instead of bearer context: %+v", listResp.Families)
	}

	rec = serveJSON(t, r, http.MethodPost, "/v1/sessions/revoke", map[string]any{"tenant_id": 7, "user_id": 7002}, issued.SessionToken)
	assertHTTPStatus(t, rec, http.StatusOK)
	if _, err := sessionSvc.Validate(context.Background(), other.SessionToken, "10.2.1.1", "Firefox/1"); err != nil {
		t.Fatalf("body user revoke should not revoke another user: %v", err)
	}
	if _, err := sessionSvc.Validate(context.Background(), issued.SessionToken, "10.1.1.1", "Chrome/1"); err == nil {
		t.Fatal("bearer user's own sessions should be revoked")
	}
}

func TestAT_AUTH_007_011_CrossUserRefreshRejected(t *testing.T) {
	now := time.Date(2026, 5, 16, 15, 0, 0, 0, time.UTC)
	sessionSvc := usersession.NewService(usersession.NewMemoryStore())
	sessionSvc.Now = func() time.Time { return now }
	sessionSvc.SigningKey = testSessionSigningKey()
	caller, err := sessionSvc.Create(context.Background(), usersession.CreateInput{
		TenantID: 7, UserID: 7001, IP: "192.0.2.1",
	})
	if err != nil {
		t.Fatalf("Create caller: %v", err)
	}
	target, err := sessionSvc.Create(context.Background(), usersession.CreateInput{
		TenantID: 7, UserID: 7002, IP: "192.0.2.1",
	})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}
	r := chi.NewRouter()
	r.Route("/v1/sessions", func(r chi.Router) {
		r.Use(sessionauth.SessionMiddleware(sessionSvc, nil))
		MountSessionRoutes(r, SessionHandlerDeps{Sessions: sessionSvc})
	})

	rec := serveJSON(t, r, http.MethodPost, "/v1/sessions/refresh", map[string]any{
		"refresh_token": target.RefreshToken,
	}, caller.SessionToken)
	assertHTTPStatus(t, rec, http.StatusUnauthorized)
	if strings.Contains(rec.Body.String(), target.RefreshToken) || strings.Contains(rec.Body.String(), caller.SessionToken) {
		t.Fatalf("response leaked token material: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "refresh_token_cross_user_attempt") {
		t.Fatalf("response did not expose safe audit code: %s", rec.Body.String())
	}

	rec = serveJSON(t, r, http.MethodPost, "/v1/sessions/refresh", map[string]any{
		"refresh_token": target.RefreshToken,
	}, target.SessionToken)
	assertHTTPStatus(t, rec, http.StatusUnauthorized)
	families, err := sessionSvc.List(context.Background(), 7, 7002)
	if err != nil {
		t.Fatalf("List target families: %v", err)
	}
	if len(families) != 1 || families[0].Status != usersession.FamilyStatusRevoked ||
		families[0].RevokedReason != "refresh_token_cross_user_attempt" {
		t.Fatalf("target family was not revoked after cross-user refresh attempt: %+v", families)
	}
}

func TestS2_011_SessionRefreshSurvivesExpiredSessionBearer(t *testing.T) {
	now := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	sessionSvc := usersession.NewService(usersession.NewMemoryStore())
	sessionSvc.Now = func() time.Time { return now }
	sessionSvc.SigningKey = testSessionSigningKey()
	sessionSvc.SessionTTL = time.Minute
	sessionSvc.RefreshTTL = time.Hour

	issued, err := sessionSvc.Create(context.Background(), usersession.CreateInput{
		TenantID: 7, UserID: 7001, IP: "192.0.2.10", UserAgent: "Chrome/1",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	r := chi.NewRouter()
	r.Route("/v1/sessions", func(r chi.Router) {
		r.Post("/refresh", newSessionRefreshHandler(SessionHandlerDeps{Sessions: sessionSvc}))
		r.Group(func(r chi.Router) {
			r.Use(sessionauth.SessionMiddleware(sessionSvc, nil))
			r.Post("/list", newSessionListHandler(SessionHandlerDeps{Sessions: sessionSvc}))
		})
	})

	now = now.Add(2 * time.Minute)
	if _, err := sessionSvc.Validate(context.Background(), issued.SessionToken, "192.0.2.10", "Chrome/1"); !errors.Is(err, usersession.ErrTokenExpired) {
		t.Fatalf("fixture session token = %v, want ErrTokenExpired", err)
	}

	rec := serveJSON(t, r, http.MethodPost, "/v1/sessions/list", map[string]any{}, issued.SessionToken)
	assertHTTPStatus(t, rec, http.StatusUnauthorized)

	rec = serveJSON(t, r, http.MethodPost, "/v1/sessions/refresh", map[string]any{
		"refresh_token": issued.RefreshToken,
	}, issued.SessionToken)
	assertHTTPStatus(t, rec, http.StatusOK)
	var refreshResp struct {
		Session usersession.IssuedTokens `json:"session"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &refreshResp); err != nil {
		t.Fatalf("decode refresh response: %v", err)
	}
	if refreshResp.Session.RefreshToken == "" || refreshResp.Session.RefreshToken == issued.RefreshToken {
		t.Fatalf("refresh token was not rotated: old=%q response=%+v", issued.RefreshToken, refreshResp.Session)
	}
	if _, err := sessionSvc.Validate(context.Background(), refreshResp.Session.SessionToken, "192.0.2.10", "Chrome/1"); err != nil {
		t.Fatalf("new session token should validate: %v", err)
	}
}

func TestAT_AUTH_007_010_AuthRedactionAcrossAuditLogAndStructuredSinks(t *testing.T) {
	now := time.Date(2026, 5, 17, 9, 0, 0, 0, time.UTC)
	authStore := newGatewayMemoryAuthStore(now)
	authSvc := userauth.NewService(authStore)
	authSvc.PasswordPolicy = userauth.PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	authSvc.Now = func() time.Time { return now }
	sessionSvc := usersession.NewService(usersession.NewMemoryStore())
	sessionSvc.Now = func() time.Time { return now }
	sessionSvc.SigningKey = testSessionSigningKey()
	email := &captureAuthEmail{}
	events := &captureAuthEventSink{}
	adminAuth := authAdminStub{ident: admin.AdminIdentity{TokenID: 5, Role: admin.RolePlatformAdmin}}
	r := chi.NewRouter()
	r.Route("/v1/auth", func(r chi.Router) {
		MountAuthRoutes(r, AuthHandlerDeps{Auth: authSvc, Sessions: sessionSvc, EmailSender: email, AdminAuth: adminAuth, EventSink: events})
	})
	r.Route("/v1/sessions", func(r chi.Router) {
		r.Use(sessionauth.SessionMiddleware(sessionSvc, nil))
		MountSessionRoutes(r, SessionHandlerDeps{Sessions: sessionSvc, EventSink: events})
	})

	var systemLog bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&systemLog, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	passwordSentinel := "AT-AUTH-007-010-password-sentinel"
	wrongPasswordSentinel := "AT-AUTH-007-010-wrong-password-sentinel"
	cookieSentinel := "AT-AUTH-007-010-cookie-sentinel"
	resetTokenSentinel := "AT-AUTH-007-010-reset-token-sentinel"
	headers := map[string]string{"Cookie": "huakai_session=" + cookieSentinel}
	scannedResponses := map[string]any{}

	t.Setenv("HUAKAI_DEV_AUTH_RETURN_TOKEN", "false")
	rec := serveJSONWithHeaders(t, r, http.MethodPost, "/v1/auth/register", map[string]any{
		"tenant_id": 1, "email": "redact@example.test", "password": passwordSentinel,
	}, headers)
	assertHTTPStatus(t, rec, http.StatusCreated)
	scannedResponses["register_success_response"] = rec.Body.String()
	if email.verification == "" {
		t.Fatal("verification token was not sent")
	}
	rec = serveJSONWithHeaders(t, r, http.MethodPost, "/v1/auth/verify-email", map[string]any{
		"tenant_id": 1, "token": email.verification,
	}, headers)
	assertHTTPStatus(t, rec, http.StatusOK)
	scannedResponses["verify_success_response"] = rec.Body.String()

	rec = serveJSONWithHeaders(t, r, http.MethodPost, "/v1/auth/login", map[string]any{
		"tenant_id": 1, "email": "redact@example.test", "password": passwordSentinel,
	}, headers)
	assertHTTPStatus(t, rec, http.StatusOK)
	assertSentinelsAbsent(t, map[string]any{"login_success_response": rec.Body.String()}, []string{passwordSentinel, cookieSentinel})
	var loginResp struct {
		Session usersession.IssuedTokens `json:"session"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if loginResp.Session.RefreshToken == "" || loginResp.Session.SessionToken == "" {
		t.Fatalf("login did not issue tokens: %+v", loginResp.Session)
	}

	rec = serveJSONWithHeaders(t, r, http.MethodPost, "/v1/auth/login", map[string]any{
		"tenant_id": 1, "email": "redact@example.test", "password": wrongPasswordSentinel,
	}, headers)
	assertHTTPStatus(t, rec, http.StatusUnauthorized)
	scannedResponses["wrong_password_response"] = rec.Body.String()

	t.Setenv("HUAKAI_DEV_AUTH_RETURN_TOKEN", "true")
	rec = serveJSONWithHeaders(t, r, http.MethodPost, "/v1/auth/reset-password", map[string]any{
		"tenant_id": 1, "email": "redact@example.test",
	}, headers)
	assertHTTPStatus(t, rec, http.StatusAccepted)
	if email.reset == "" {
		t.Fatal("reset token was not sent")
	}

	t.Setenv("HUAKAI_DEV_AUTH_RETURN_TOKEN", "false")
	rec = serveJSONWithHeaders(t, r, http.MethodPost, "/v1/auth/reset-password", map[string]any{
		"tenant_id": 1, "token": email.reset, "new_password": passwordSentinel + "-rotated",
	}, headers)
	assertHTTPStatus(t, rec, http.StatusOK)
	scannedResponses["reset_success_response"] = rec.Body.String()

	rec = serveJSONWithHeaders(t, r, http.MethodPost, "/v1/auth/reset-password", map[string]any{
		"tenant_id": 1, "token": resetTokenSentinel, "new_password": passwordSentinel + "-bad",
	}, headers)
	assertHTTPStatus(t, rec, http.StatusBadRequest)
	scannedResponses["reset_failure_response"] = rec.Body.String()

	caller, err := sessionSvc.Create(context.Background(), usersession.CreateInput{
		TenantID: 1, UserID: 9001, IP: "192.0.2.1", UserAgent: "Chrome/1",
	})
	if err != nil {
		t.Fatalf("Create caller: %v", err)
	}
	target, err := sessionSvc.Create(context.Background(), usersession.CreateInput{
		TenantID: 1, UserID: 9002, IP: "192.0.2.2", UserAgent: "Firefox/1",
	})
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}
	rec = serveJSONWithHeaders(t, r, http.MethodPost, "/v1/sessions/refresh", map[string]any{
		"refresh_token": target.RefreshToken,
	}, map[string]string{
		"Authorization": "Bearer " + caller.SessionToken,
		"Cookie":        "huakai_session=" + cookieSentinel,
	})
	assertHTTPStatus(t, rec, http.StatusUnauthorized)
	scannedResponses["cross_user_refresh_response"] = rec.Body.String()

	backendErrStore := &gatewayBackendErrorAuthStore{
		gatewayMemoryAuthStore: newGatewayMemoryAuthStore(now),
		err:                    errors.New("backend echoed " + passwordSentinel + " " + cookieSentinel),
	}
	backendErrSvc := userauth.NewService(backendErrStore)
	backendErrRouter := chi.NewRouter()
	backendErrRouter.Route("/v1/auth", func(r chi.Router) {
		MountAuthRoutes(r, AuthHandlerDeps{Auth: backendErrSvc, Sessions: sessionSvc, EventSink: events})
	})
	rec = serveJSONWithHeaders(t, backendErrRouter, http.MethodPost, "/v1/auth/login", map[string]any{
		"tenant_id": 1, "email": "redact@example.test", "password": passwordSentinel,
	}, headers)
	assertHTTPStatus(t, rec, http.StatusServiceUnavailable)
	scannedResponses["auth_backend_error_response"] = rec.Body.String()

	sentinels := []string{
		passwordSentinel,
		wrongPasswordSentinel,
		cookieSentinel,
		resetTokenSentinel,
		email.verification,
		email.reset,
		loginResp.Session.SessionToken,
		loginResp.Session.RefreshToken,
		caller.SessionToken,
		caller.RefreshToken,
		target.SessionToken,
		target.RefreshToken,
	}
	assertSentinelsAbsent(t, scannedResponses, sentinels)
	assertSentinelsAbsent(t, map[string]any{
		"system_logger_output": systemLog.String(),
		"auth_event_sinks":     events.SinkPayloads(),
	}, sentinels)
}

func TestAT_AUTH_007_009_SocialIdentityChangeRevokesExistingSessions(t *testing.T) {
	now := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	authStore := newGatewayMemoryAuthStore(now)
	authSvc := userauth.NewService(authStore)
	authSvc.Now = func() time.Time { return now }
	authSvc.OAuth = userauth.NewOAuthService(&gatewayFakeOAuthProvider{
		provider: userauth.SocialProviderGoogle,
		identity: userauth.VerifiedIdentity{
			Provider: userauth.SocialProviderGoogle, Subject: "google-social-subject",
			Email: "social@example.test", DisplayName: "Social", EmailVerified: true,
		},
	})
	sessionSvc := usersession.NewService(usersession.NewMemoryStore())
	sessionSvc.Now = func() time.Time { return now }
	sessionSvc.SigningKey = testSessionSigningKey()
	events := &captureAuthEventSink{}

	blockedRouter := chi.NewRouter()
	blockedRouter.Route("/v1/auth", func(r chi.Router) {
		MountAuthRoutes(r, AuthHandlerDeps{
			Auth: authSvc, Sessions: sessionSvc, EventSink: events,
			AdminAuth: authAdminStub{ident: admin.AdminIdentity{TokenID: 7, Role: admin.RoleTenantOperator, ScopeTenantID: 2}},
		})
	})
	allowedRouter := chi.NewRouter()
	allowedRouter.Route("/v1/auth", func(r chi.Router) {
		MountAuthRoutes(r, AuthHandlerDeps{
			Auth: authSvc, Sessions: sessionSvc, EventSink: events,
			AdminAuth: authAdminStub{ident: admin.AdminIdentity{TokenID: 8, Role: admin.RoleTenantOperator, ScopeTenantID: 1}},
		})
	})

	rec := serveJSON(t, allowedRouter, http.MethodPost, "/v1/auth/oauth-init", map[string]any{
		"tenant_id": 1, "provider": userauth.SocialProviderGoogle,
	})
	assertHTTPStatus(t, rec, http.StatusCreated)
	var initResp userauth.OAuthInitResult
	if err := json.Unmarshal(rec.Body.Bytes(), &initResp); err != nil {
		t.Fatalf("decode oauth init: %v", err)
	}
	rec = serveJSON(t, allowedRouter, http.MethodPost, "/v1/auth/oauth-callback", map[string]any{
		"tenant_id": 1, "provider": userauth.SocialProviderGoogle, "state": initResp.State, "code": "provider-code",
	})
	assertHTTPStatus(t, rec, http.StatusOK)
	var callbackResp struct {
		User    map[string]any           `json:"user"`
		Session usersession.IssuedTokens `json:"session"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &callbackResp); err != nil {
		t.Fatalf("decode oauth callback: %v", err)
	}
	userIDFloat, ok := callbackResp.User["id"].(float64)
	if !ok || userIDFloat == 0 {
		t.Fatalf("oauth callback missing user id: %+v", callbackResp.User)
	}
	userID := int64(userIDFloat)
	if _, err := sessionSvc.Validate(context.Background(), callbackResp.Session.SessionToken, "192.0.2.1", ""); err != nil {
		t.Fatalf("social session should initially validate: %v", err)
	}

	body := map[string]any{
		"tenant_id": 1, "user_id": userID, "provider": userauth.SocialProviderGoogle,
		"subject": "google-social-subject", "change_type": "provider_disabled",
	}
	rec = serveJSON(t, blockedRouter, http.MethodPost, "/v1/auth/social/identity-changed", body)
	assertHTTPStatus(t, rec, http.StatusForbidden)
	if _, err := sessionSvc.Validate(context.Background(), callbackResp.Session.SessionToken, "192.0.2.1", ""); err != nil {
		t.Fatalf("cross-tenant blocked webhook should not revoke session: %v", err)
	}

	rec = serveJSON(t, allowedRouter, http.MethodPost, "/v1/auth/social/identity-changed", body)
	assertHTTPStatus(t, rec, http.StatusOK)
	var changedResp struct {
		SessionPolicy   string `json:"session_policy"`
		ReasonClass     string `json:"reason_class"`
		SessionsRevoked int64  `json:"sessions_revoked"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &changedResp); err != nil {
		t.Fatalf("decode identity changed response: %v", err)
	}
	if changedResp.SessionPolicy != "revoked" || changedResp.ReasonClass != "social_identity_provider_disabled" || changedResp.SessionsRevoked != 1 {
		t.Fatalf("unexpected identity change response: %+v", changedResp)
	}
	if _, err := sessionSvc.Validate(context.Background(), callbackResp.Session.SessionToken, "192.0.2.1", ""); !errors.Is(err, usersession.ErrFamilyRevoked) {
		t.Fatalf("old social session after identity change = %v, want ErrFamilyRevoked", err)
	}
	if _, err := sessionSvc.Refresh(context.Background(), usersession.RefreshInput{
		TenantID: 1, UserID: userID, RefreshToken: callbackResp.Session.RefreshToken,
	}); !errors.Is(err, usersession.ErrFamilyRevoked) {
		t.Fatalf("old social refresh after identity change = %v, want ErrFamilyRevoked", err)
	}
	families, err := sessionSvc.List(context.Background(), 1, userID)
	if err != nil {
		t.Fatalf("List families: %v", err)
	}
	if len(families) != 1 || families[0].RevokedReason != "social_identity_provider_disabled" {
		t.Fatalf("identity change revoke reason mismatch: %+v", families)
	}
}

type captureAuthEmail struct {
	verification string
	reset        string
}

func (c *captureAuthEmail) SendVerification(_ context.Context, _ userauth.User, token string) error {
	c.verification = token
	return nil
}

func (c *captureAuthEmail) SendPasswordReset(_ context.Context, _ userauth.User, token string) error {
	c.reset = token
	return nil
}

func serveJSON(t *testing.T, h http.Handler, method, target string, body any, bearer ...string) *httptest.ResponseRecorder {
	t.Helper()
	headers := map[string]string{}
	if len(bearer) > 0 && strings.TrimSpace(bearer[0]) != "" {
		headers["Authorization"] = "Bearer " + bearer[0]
	}
	return serveJSONWithHeaders(t, h, method, target, body, headers)
}

func serveJSONWithHeaders(t *testing.T, h http.Handler, method, target string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func assertHTTPStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d want %d body=%s", rec.Code, want, rec.Body.String())
	}
}

func newGatewayMemoryAuthStore(now time.Time) *gatewayMemoryAuthStore {
	return &gatewayMemoryAuthStore{
		now:         now,
		nextID:      1,
		users:       map[int64]userauth.User{},
		byEmail:     map[string]int64{},
		emailTokens: map[string]userauth.TokenChallenge{},
		resetTokens: map[string]gatewayResetChallenge{},
		invites:     map[string]userauth.InviteCode{},
		oauthFlows:  map[string]userauth.OAuthFlowSession{},
		socialLinks: map[string]int64{},
	}
}

type authAdminStub struct {
	ident admin.AdminIdentity
	err   error
}

func (a authAdminStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if a.err != nil {
		return admin.AdminIdentity{}, a.err
	}
	return a.ident, nil
}

type gatewayFakeOAuthProvider struct {
	provider string
	identity userauth.VerifiedIdentity
}

func (p *gatewayFakeOAuthProvider) Provider() string { return p.provider }

func (p *gatewayFakeOAuthProvider) AuthorizationURL(challenge userauth.OAuthFlowChallenge) (string, error) {
	return "https://auth.example.test/authorize?state=" + challenge.State, nil
}

func (p *gatewayFakeOAuthProvider) ExchangeVerifiedIdentity(_ context.Context, flow userauth.OAuthFlowSession, code string) (userauth.VerifiedIdentity, error) {
	if strings.TrimSpace(code) == "" || flow.PKCEVerifier == "" {
		return userauth.VerifiedIdentity{}, userauth.ErrSocialLoginRejected
	}
	return p.identity, nil
}

type captureAuthEventSink struct {
	mu     sync.Mutex
	events []AuthEvent
}

func (s *captureAuthEventSink) RecordAuthEvent(_ context.Context, event AuthEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *captureAuthEventSink) SinkPayloads() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	auditLog := make([]map[string]any, 0, len(s.events))
	systemLog := make([]map[string]any, 0, len(s.events))
	userActionLog := make([]map[string]any, 0, len(s.events))
	trustLedger := make([]map[string]any, 0, len(s.events))
	channelHealthAudit := make([]map[string]any, 0, len(s.events))
	for _, event := range s.events {
		auditLog = append(auditLog, map[string]any{
			"event_type":       event.EventType,
			"tenant_id":        event.TenantID,
			"user_id":          event.UserID,
			"provider":         event.Provider,
			"outcome":          event.Outcome,
			"reason_class":     event.ReasonClass,
			"auth_method":      event.AuthMethod,
			"session_policy":   event.SessionPolicy,
			"sessions_revoked": event.SessionsRevoked,
		})
		systemLog = append(systemLog, map[string]any{
			"component":    "auth",
			"event_type":   event.EventType,
			"outcome":      event.Outcome,
			"reason_class": event.ReasonClass,
		})
		userActionLog = append(userActionLog, map[string]any{
			"event_type":   event.EventType,
			"tenant_id":    event.TenantID,
			"user_id":      event.UserID,
			"outcome":      event.Outcome,
			"reason_class": event.ReasonClass,
		})
		trustLedger = append(trustLedger, map[string]any{
			"hop_chain": []map[string]any{{
				"hop_kind":     "auth",
				"decision_ref": event.EventType + ":" + event.Outcome + ":" + event.ReasonClass,
			}},
		})
		channelHealthAudit = append(channelHealthAudit, map[string]any{
			"event_type":   event.EventType,
			"tenant_id":    event.TenantID,
			"reason_class": event.ReasonClass,
			"outcome":      event.Outcome,
		})
	}
	return map[string]any{
		"audit_log":            auditLog,
		"system_log":           systemLog,
		"user_action_log":      userActionLog,
		"f_trust_ledger":       trustLedger,
		"channel_health_audit": channelHealthAudit,
	}
}

func assertSentinelsAbsent(t *testing.T, sinks map[string]any, sentinels []string) {
	t.Helper()
	for name, sink := range sinks {
		raw := sinkText(t, sink)
		for _, sentinel := range sentinels {
			if strings.TrimSpace(sentinel) == "" {
				continue
			}
			if strings.Contains(raw, sentinel) {
				t.Fatalf("%s leaked sentinel %q in %s", name, sentinel, raw)
			}
		}
	}
}

func sinkText(t *testing.T, sink any) string {
	t.Helper()
	switch v := sink.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal sink: %v", err)
		}
		return string(raw)
	}
}

type gatewayBackendErrorAuthStore struct {
	*gatewayMemoryAuthStore
	err error
}

func (s *gatewayBackendErrorAuthStore) GetUserByEmail(context.Context, int64, string) (userauth.User, error) {
	return userauth.User{}, s.err
}

type gatewayMemoryAuthStore struct {
	now         time.Time
	nextID      int64
	users       map[int64]userauth.User
	byEmail     map[string]int64
	emailTokens map[string]userauth.TokenChallenge
	resetTokens map[string]gatewayResetChallenge
	invites     map[string]userauth.InviteCode
	oauthFlows  map[string]userauth.OAuthFlowSession
	socialLinks map[string]int64
}

type gatewayResetChallenge struct {
	userauth.TokenChallenge
	PasswordVersion int
	Consumed        bool
}

func (s *gatewayMemoryAuthStore) CreateUser(_ context.Context, in userauth.CreateUserParams) (userauth.User, error) {
	email := userauth.NormalizeEmail(in.Email)
	user := userauth.User{
		ID: in.TenantID*1000 + s.nextID, TenantID: in.TenantID, Email: email, DisplayName: in.DisplayName,
		PasswordHash: in.PasswordHash, EmailVerified: in.EmailVerified, InviteCodeUsed: in.InviteCodeUsed,
		SocialLoginProvider: in.SocialLoginProvider, Status: in.Status, PasswordVersion: 1,
		CreatedAt: s.now, UpdatedAt: s.now,
	}
	if user.Status == "" {
		user.Status = userauth.UserStatusPendingVerification
	}
	s.nextID++
	s.users[user.ID] = user
	s.byEmail[email] = user.ID
	return user, nil
}

func (s *gatewayMemoryAuthStore) GetUserByEmail(_ context.Context, _ int64, email string) (userauth.User, error) {
	id, ok := s.byEmail[userauth.NormalizeEmail(email)]
	if !ok {
		return userauth.User{}, userauth.ErrUserNotFound
	}
	return s.users[id], nil
}

func (s *gatewayMemoryAuthStore) GetUserByID(_ context.Context, tenantID, userID int64) (userauth.User, error) {
	user, ok := s.users[userID]
	if !ok || user.TenantID != tenantID {
		return userauth.User{}, userauth.ErrUserNotFound
	}
	return user, nil
}

func (s *gatewayMemoryAuthStore) MarkLoginSuccess(_ context.Context, tenantID, userID int64) error {
	user := s.users[userID]
	if user.TenantID == tenantID {
		user.FailedLoginCount = 0
		s.users[userID] = user
	}
	return nil
}
func (s *gatewayMemoryAuthStore) MarkLoginFailure(_ context.Context, tenantID, userID int64, threshold int) error {
	user := s.users[userID]
	if user.TenantID == tenantID {
		user.FailedLoginCount++
		if user.FailedLoginCount >= threshold {
			user.Status = userauth.UserStatusLocked
		}
		s.users[userID] = user
	}
	return nil
}

func (s *gatewayMemoryAuthStore) GetUserBySocialIdentity(_ context.Context, tenantID int64, provider, subject string) (userauth.User, error) {
	id, ok := s.socialLinks[authTestKey(tenantID, userauth.NormalizeEmail(provider+":"+subject))]
	if !ok {
		return userauth.User{}, userauth.ErrUserNotFound
	}
	return s.users[id], nil
}

func (s *gatewayMemoryAuthStore) LinkSocialIdentity(_ context.Context, tenantID, userID int64, provider, subject string) (userauth.User, error) {
	user := s.users[userID]
	if user.TenantID != tenantID {
		return userauth.User{}, userauth.ErrUserNotFound
	}
	user.SocialLoginProvider = provider
	user.EmailVerified = true
	user.Status = userauth.UserStatusActive
	s.users[user.ID] = user
	s.socialLinks[authTestKey(tenantID, userauth.NormalizeEmail(provider+":"+subject))] = user.ID
	return user, nil
}

func (s *gatewayMemoryAuthStore) CreateEmailVerificationToken(_ context.Context, challenge userauth.TokenChallenge) error {
	s.emailTokens[string(challenge.TokenHash)] = challenge
	return nil
}

func (s *gatewayMemoryAuthStore) ConsumeEmailVerificationToken(_ context.Context, tenantID int64, tokenHash []byte, now time.Time) (userauth.User, error) {
	challenge, ok := s.emailTokens[string(tokenHash)]
	if !ok || challenge.TenantID != tenantID || !challenge.ExpiresAt.After(now) {
		return userauth.User{}, userauth.ErrTokenInvalid
	}
	delete(s.emailTokens, string(tokenHash))
	user := s.users[challenge.UserID]
	user.EmailVerified = true
	user.Status = userauth.UserStatusActive
	user.UpdatedAt = now
	s.users[user.ID] = user
	return user, nil
}

func (s *gatewayMemoryAuthStore) CreatePasswordResetToken(_ context.Context, challenge userauth.TokenChallenge, passwordVersion int) error {
	s.resetTokens[string(challenge.TokenHash)] = gatewayResetChallenge{TokenChallenge: challenge, PasswordVersion: passwordVersion}
	return nil
}

func (s *gatewayMemoryAuthStore) PreparePasswordResetTokenUser(_ context.Context, tenantID int64, tokenHash []byte, now time.Time) (userauth.User, error) {
	challenge, ok := s.resetTokens[string(tokenHash)]
	if !ok || challenge.Consumed || challenge.TenantID != tenantID || !challenge.ExpiresAt.After(now) {
		return userauth.User{}, userauth.ErrTokenInvalid
	}
	user := s.users[challenge.UserID]
	if user.PasswordVersion != challenge.PasswordVersion {
		return userauth.User{}, userauth.ErrTokenInvalid
	}
	switch user.Status {
	case userauth.UserStatusActive, userauth.UserStatusLocked, userauth.UserStatusPendingVerification:
		user.Status = userauth.UserStatusResetRequired
		user.UpdatedAt = now
		s.users[user.ID] = user
	}
	return user, nil
}

func (s *gatewayMemoryAuthStore) ConsumePasswordResetToken(_ context.Context, tenantID int64, tokenHash []byte, passwordHash string, now time.Time) (userauth.User, error) {
	challenge, ok := s.resetTokens[string(tokenHash)]
	if !ok || challenge.Consumed || challenge.TenantID != tenantID || !challenge.ExpiresAt.After(now) {
		return userauth.User{}, userauth.ErrTokenInvalid
	}
	user := s.users[challenge.UserID]
	user.PasswordHash = passwordHash
	user.PasswordVersion++
	if user.Status == userauth.UserStatusLocked || user.Status == userauth.UserStatusResetRequired || user.Status == userauth.UserStatusPendingVerification {
		user.Status = userauth.UserStatusActive
	}
	user.UpdatedAt = now
	challenge.Consumed = true
	s.resetTokens[string(tokenHash)] = challenge
	s.users[user.ID] = user
	return user, nil
}

func (s *gatewayMemoryAuthStore) RedeemInvite(_ context.Context, tenantID int64, rawCode string, now time.Time) (userauth.InviteCode, error) {
	hash := userauth.HashInviteCode(rawCode)
	invite, ok := s.invites[hash]
	if !ok || invite.TenantID != tenantID || invite.Status != "active" || invite.UsedCount >= invite.MaxUses {
		return userauth.InviteCode{}, userauth.ErrInviteInvalid
	}
	if invite.ValidUntil != nil && !invite.ValidUntil.After(now) {
		return userauth.InviteCode{}, userauth.ErrInviteInvalid
	}
	invite.UsedCount++
	s.invites[hash] = invite
	return invite, nil
}

func (s *gatewayMemoryAuthStore) CreateInviteBinding(context.Context, int64, int64, string, time.Time) error {
	return nil
}

func (s *gatewayMemoryAuthStore) CreateOAuthFlowSession(_ context.Context, challenge userauth.OAuthFlowChallenge) error {
	s.oauthFlows[string(challenge.StateHash)] = userauth.OAuthFlowSession{
		ID: challenge.ID, TenantID: challenge.TenantID, Provider: challenge.Provider,
		StateHash: challenge.StateHash, NonceHash: challenge.NonceHash, PKCEVerifier: challenge.PKCEVerifier,
		RedirectURI: challenge.RedirectURI, ExpiresAt: challenge.ExpiresAt, CreatedAt: s.now,
	}
	return nil
}

func (s *gatewayMemoryAuthStore) ConsumeOAuthFlowSession(_ context.Context, tenantID int64, provider string, stateHash []byte, now time.Time) (userauth.OAuthFlowSession, error) {
	flow, ok := s.oauthFlows[string(stateHash)]
	if !ok || flow.TenantID != tenantID || flow.Provider != provider {
		return userauth.OAuthFlowSession{}, userauth.ErrOAuthFlowNotFound
	}
	if flow.ConsumedAt != nil || !flow.ExpiresAt.After(now) {
		return userauth.OAuthFlowSession{}, userauth.ErrOAuthFlowExpired
	}
	t := now.UTC()
	flow.ConsumedAt = &t
	s.oauthFlows[string(stateHash)] = flow
	return flow, nil
}

func authTestKey(tenantID int64, value string) string {
	return strconv.FormatInt(tenantID, 10) + ":" + strings.TrimSpace(value)
}

func testSessionSigningKey() []byte {
	return []byte("0123456789abcdef0123456789abcdef")
}

// flakyRevokeSessionStore embeds a working memory store but forces the first user-scope revoke to
// fail, driving the S1-028 "revoke failed before password change" path while proving the same reset
// token can be retried after the session backend recovers.
type flakyRevokeSessionStore struct {
	usersession.Store
	failures           int
	injectAfterSuccess bool
	injected           bool
}

func (s *flakyRevokeSessionStore) RevokeUser(ctx context.Context, tenantID, userID int64, reason string, now time.Time) (int64, error) {
	if s.failures > 0 {
		s.failures--
		return 0, errors.New("revoke boom")
	}
	count, err := s.Store.RevokeUser(ctx, tenantID, userID, reason, now)
	if err != nil {
		return count, err
	}
	if s.injectAfterSuccess && !s.injected {
		s.injected = true
		if _, createErr := s.Store.CreateFamily(ctx, usersession.CreateInput{
			TenantID: tenantID, UserID: userID, IP: "192.0.2.99", UserAgent: "RaceLogin/1",
		}, now); createErr != nil {
			return count, createErr
		}
	}
	return count, nil
}

type postCommitRevokeFailSessionStore struct {
	usersession.Store
	calls int
}

func (s *postCommitRevokeFailSessionStore) RevokeUser(ctx context.Context, tenantID, userID int64, reason string, now time.Time) (int64, error) {
	s.calls++
	if s.calls == 2 {
		return 0, errors.New("post revoke boom")
	}
	return s.Store.RevokeUser(ctx, tenantID, userID, reason, now)
}

// resetConfirmTestSetup registers + verifies a user and returns a live router plus the captured
// reset token, so reset-confirm tests can exercise the real handler path.
func resetConfirmTestSetup(t *testing.T, sessions *usersession.Service) (http.Handler, *captureAuthEmail, *userauth.Service) {
	t.Helper()
	now := time.Date(2026, 5, 17, 11, 0, 0, 0, time.UTC)
	authStore := newGatewayMemoryAuthStore(now)
	authSvc := userauth.NewService(authStore)
	authSvc.PasswordPolicy = userauth.PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	authSvc.Now = func() time.Time { return now }
	email := &captureAuthEmail{}
	r := chi.NewRouter()
	r.Route("/v1/auth", func(r chi.Router) {
		MountAuthRoutes(r, AuthHandlerDeps{Auth: authSvc, Sessions: sessions, EmailSender: email})
	})
	t.Setenv("HUAKAI_DEV_AUTH_RETURN_TOKEN", "true")
	rec := serveJSON(t, r, http.MethodPost, "/v1/auth/register", map[string]any{"tenant_id": 1, "email": "reset@example.test", "password": "secret-old"})
	assertHTTPStatus(t, rec, http.StatusCreated)
	rec = serveJSON(t, r, http.MethodPost, "/v1/auth/verify-email", map[string]any{"tenant_id": 1, "token": email.verification})
	assertHTTPStatus(t, rec, http.StatusOK)
	return r, email, authSvc
}

// TestAuthPasswordResetConfirmFailsClosedWhenRevokeFails guards S1-028: reset-confirm must place
// the reset subject behind a login barrier, then refuse to consume the one-time token or change the
// password unless old sessions were revoked first. The injected store fails ONLY the first
// RevokeUser, then simulates an in-flight old-password login by creating a new active family after
// the first successful revoke. The fixture distinguishes a correct fail-closed/retryable handler
// with a post-commit sweep from the broken post-reset best-effort revoke path and from a pre-revoke
// implementation that leaves old-password login open while waiting for retry.
//
// Mutation check: move ResetPassword before Revoke, ignore the Revoke error, or remove the reset
// login barrier, or remove the post-commit revoke sweep; either the first response becomes 2xx, the
// new password authenticates too early, old-password login remains possible before retry, or the
// injected race family remains active after retry -> red.
func TestAuthPasswordResetConfirmFailsClosedWhenRevokeFails(t *testing.T) {
	sessionStore := &flakyRevokeSessionStore{Store: usersession.NewMemoryStore(), failures: 1, injectAfterSuccess: true}
	sessionSvc := usersession.NewService(sessionStore)
	sessionSvc.Now = func() time.Time { return time.Date(2026, 5, 17, 11, 0, 0, 0, time.UTC) }
	sessionSvc.SigningKey = testSessionSigningKey()
	r, email, authSvc := resetConfirmTestSetup(t, sessionSvc)
	user, err := authSvc.Authenticate(context.Background(), userauth.LoginInput{TenantID: 1, Email: "reset@example.test", Password: "secret-old"})
	if err != nil {
		t.Fatalf("Authenticate before reset: %v", err)
	}
	issued, err := sessionSvc.Create(context.Background(), usersession.CreateInput{
		TenantID: 1, UserID: user.ID, IP: "192.0.2.1", UserAgent: "Chrome/1",
	})
	if err != nil {
		t.Fatalf("Create session before reset: %v", err)
	}

	rec := serveJSON(t, r, http.MethodPost, "/v1/auth/reset-password", map[string]any{"tenant_id": 1, "email": "reset@example.test"})
	assertHTTPStatus(t, rec, http.StatusAccepted)
	if email.reset == "" {
		t.Fatal("reset token was not issued")
	}
	rec = serveJSON(t, r, http.MethodPost, "/v1/auth/reset-password", map[string]any{
		"tenant_id": 1, "token": email.reset, "new_password": "secret-rotated",
	})
	if rec.Code/100 == 2 {
		t.Fatalf("reset-confirm must fail closed when session revocation fails; got %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := authSvc.Authenticate(context.Background(), userauth.LoginInput{TenantID: 1, Email: "reset@example.test", Password: "secret-old"}); !errors.Is(err, userauth.ErrPasswordResetRequired) {
		t.Fatalf("old password login must be blocked by reset barrier after revoke failure; got %v", err)
	}
	if _, err := authSvc.Authenticate(context.Background(), userauth.LoginInput{TenantID: 1, Email: "reset@example.test", Password: "secret-rotated"}); !errors.Is(err, userauth.ErrPasswordResetRequired) {
		t.Fatalf("new password login must also be blocked until retry consumes the token; got %v", err)
	}
	if _, err := sessionSvc.Validate(context.Background(), issued.SessionToken, "192.0.2.1", "Chrome/1"); err != nil {
		t.Fatalf("pre-existing session should remain valid because reset did not complete; got %v", err)
	}
	rec = serveJSON(t, r, http.MethodPost, "/v1/auth/reset-password", map[string]any{
		"tenant_id": 1, "token": email.reset, "new_password": "secret-rotated",
	})
	assertHTTPStatus(t, rec, http.StatusOK)
	if _, err := authSvc.Authenticate(context.Background(), userauth.LoginInput{TenantID: 1, Email: "reset@example.test", Password: "secret-rotated"}); err != nil {
		t.Fatalf("new password must authenticate after retry revokes sessions and consumes token; got %v", err)
	}
	if _, err := sessionSvc.Validate(context.Background(), issued.SessionToken, "192.0.2.1", "Chrome/1"); !errors.Is(err, usersession.ErrFamilyRevoked) {
		t.Fatalf("pre-existing session after successful retry = %v, want ErrFamilyRevoked", err)
	}
	families, err := sessionSvc.List(context.Background(), 1, user.ID)
	if err != nil {
		t.Fatalf("List families after retry: %v", err)
	}
	for _, family := range families {
		if family.Status == usersession.FamilyStatusActive || family.Status == usersession.FamilyStatusSuspicious {
			t.Fatalf("family %s remained %s after reset retry; all old/race sessions must be revoked: %+v", family.ID, family.Status, families)
		}
	}
}

// TestAuthPasswordResetConfirmPostSweepFailureIsDegradedSuccess guards the committed-token half of
// S1-028: once ResetPassword has changed the password and consumed the one-time token, a later
// post-commit sweep failure must be reported as a degraded reset success, not as a retryable 5xx.
//
// Mutation check: return writeSessionError after ResetPassword or always report "revoked"; the
// status/session_revocation assertions go red while the new password proves the token was consumed.
func TestAuthPasswordResetConfirmPostSweepFailureIsDegradedSuccess(t *testing.T) {
	sessionStore := &postCommitRevokeFailSessionStore{Store: usersession.NewMemoryStore()}
	sessionSvc := usersession.NewService(sessionStore)
	sessionSvc.Now = func() time.Time { return time.Date(2026, 5, 17, 11, 0, 0, 0, time.UTC) }
	sessionSvc.SigningKey = testSessionSigningKey()
	r, email, authSvc := resetConfirmTestSetup(t, sessionSvc)
	user, err := authSvc.Authenticate(context.Background(), userauth.LoginInput{TenantID: 1, Email: "reset@example.test", Password: "secret-old"})
	if err != nil {
		t.Fatalf("Authenticate before reset: %v", err)
	}
	issued, err := sessionSvc.Create(context.Background(), usersession.CreateInput{
		TenantID: 1, UserID: user.ID, IP: "192.0.2.1", UserAgent: "Chrome/1",
	})
	if err != nil {
		t.Fatalf("Create session before reset: %v", err)
	}

	rec := serveJSON(t, r, http.MethodPost, "/v1/auth/reset-password", map[string]any{"tenant_id": 1, "email": "reset@example.test"})
	assertHTTPStatus(t, rec, http.StatusAccepted)
	rec = serveJSON(t, r, http.MethodPost, "/v1/auth/reset-password", map[string]any{
		"tenant_id": 1, "token": email.reset, "new_password": "secret-rotated",
	})
	assertHTTPStatus(t, rec, http.StatusOK)
	var resp struct {
		SessionRevocation string `json:"session_revocation"`
		SessionsRevoked   int64  `json:"sessions_revoked"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	if resp.SessionRevocation != "failed" || resp.SessionsRevoked != 1 {
		t.Fatalf("post-sweep failure must be explicit degraded success; got %+v body=%s", resp, rec.Body.String())
	}
	if _, err := authSvc.Authenticate(context.Background(), userauth.LoginInput{TenantID: 1, Email: "reset@example.test", Password: "secret-rotated"}); err != nil {
		t.Fatalf("new password must authenticate because reset token was consumed; got %v", err)
	}
	if _, err := sessionSvc.Validate(context.Background(), issued.SessionToken, "192.0.2.1", "Chrome/1"); !errors.Is(err, usersession.ErrFamilyRevoked) {
		t.Fatalf("pre-existing session after degraded success = %v, want ErrFamilyRevoked", err)
	}
}

// TestAuthPasswordResetConfirmRequiresSessions guards S1-028: when no session store is wired,
// reset-confirm must refuse (it cannot revoke sessions) and must NOT change the password — the
// guard runs BEFORE ResetPassword. The old code skipped revocation when Sessions==nil yet still
// reset the password and reported success with SessionPolicy "revoked".
//
// Mutation check: remove the `if d.Sessions == nil` guard; the password is then changed (old
// password stops working) → the "old password still authenticates" assertion goes red.
func TestAuthPasswordResetConfirmRequiresSessions(t *testing.T) {
	r, email, authSvc := resetConfirmTestSetup(t, nil) // no session store wired

	rec := serveJSON(t, r, http.MethodPost, "/v1/auth/reset-password", map[string]any{"tenant_id": 1, "email": "reset@example.test"})
	assertHTTPStatus(t, rec, http.StatusAccepted)
	if email.reset == "" {
		t.Fatal("reset token was not issued")
	}
	rec = serveJSON(t, r, http.MethodPost, "/v1/auth/reset-password", map[string]any{
		"tenant_id": 1, "token": email.reset, "new_password": "secret-rotated",
	})
	if rec.Code/100 == 2 {
		t.Fatalf("reset-confirm without a session store must refuse, not succeed; got %d body=%s", rec.Code, rec.Body.String())
	}
	// The real discriminator: the password must be UNCHANGED (guard ran before ResetPassword).
	if _, err := authSvc.Authenticate(context.Background(), userauth.LoginInput{TenantID: 1, Email: "reset@example.test", Password: "secret-old"}); err != nil {
		t.Fatalf("old password must still authenticate — reset must not change the password when sessions cannot be revoked; got %v", err)
	}
}

// TestAuthPasswordResetConfirmRefusesWhenSessionStoreUnset guards S1-028 (codex round 2): a non-nil
// session Service whose backing Store is unset (NewService(nil)) must STILL be rejected before the
// password is changed — a bare service-pointer check is insufficient, because Revoke would then fail
// with ErrStoreNotConfigured only after the one-time token is consumed and the password changed.
//
// Mutation check: drop the `|| d.Sessions.Store == nil` clause from the guard; the reset proceeds
// (old password stops working) → the "old password still authenticates" assertion goes red.
func TestAuthPasswordResetConfirmRefusesWhenSessionStoreUnset(t *testing.T) {
	r, email, authSvc := resetConfirmTestSetup(t, usersession.NewService(nil)) // service set, backing store unset

	rec := serveJSON(t, r, http.MethodPost, "/v1/auth/reset-password", map[string]any{"tenant_id": 1, "email": "reset@example.test"})
	assertHTTPStatus(t, rec, http.StatusAccepted)
	if email.reset == "" {
		t.Fatal("reset token was not issued")
	}
	rec = serveJSON(t, r, http.MethodPost, "/v1/auth/reset-password", map[string]any{
		"tenant_id": 1, "token": email.reset, "new_password": "secret-rotated",
	})
	if rec.Code/100 == 2 {
		t.Fatalf("reset-confirm with an unconfigured session store must refuse before changing the password; got %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := authSvc.Authenticate(context.Background(), userauth.LoginInput{TenantID: 1, Email: "reset@example.test", Password: "secret-old"}); err != nil {
		t.Fatalf("old password must still authenticate — reset must not commit when the session store is unset; got %v", err)
	}
}
