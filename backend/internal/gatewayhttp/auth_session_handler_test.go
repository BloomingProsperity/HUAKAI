package gatewayhttp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/captcha"
	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/loginthrottle"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/twofa"
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

func TestAuthRegister_CaptchaFailureRejectsBeforeUserCreate(t *testing.T) {
	now := time.Date(2026, 6, 3, 9, 0, 0, 0, time.UTC)
	store := newGatewayMemoryAuthStore(now)
	authSvc := userauth.NewService(store)
	authSvc.PasswordPolicy = userauth.PasswordPolicy{
		MemoryKiB: 64, Iterations: 1, Parallelism: 1,
		SaltBytes: 8, KeyBytes: 16,
	}
	authSvc.Now = func() time.Time { return now }
	gate := &authCaptchaStub{err: captcha.ErrTokenRequired}
	r := chi.NewRouter()
	r.Route("/v1/auth", func(r chi.Router) {
		MountAuthRoutes(r, AuthHandlerDeps{Auth: authSvc, Captcha: gate})
	})

	rec := serveJSON(t, r, http.MethodPost, "/v1/auth/register", map[string]any{
		"tenant_id": 1, "email": "bot@example.test", "password": "secret",
	})

	assertHTTPStatus(t, rec, http.StatusForbidden)
	if code := loginErrorCode(t, rec); code != "captcha_required" {
		t.Fatalf("error code = %q want captcha_required", code)
	}
	if got := gate.calls(); got != 1 {
		t.Fatalf("captcha calls = %d want 1", got)
	}
	if got := gate.lastToken(); got != "" {
		t.Fatalf("captcha token = %q want empty", got)
	}
	_, err := store.GetUserByEmail(context.Background(), 1, "bot@example.test")
	if !errors.Is(err, userauth.ErrUserNotFound) {
		t.Fatalf("captcha failure must not create user, lookup err=%v", err)
	}
}

func TestAuthRegister_CaptchaSuccessAllowsUserCreate(t *testing.T) {
	now := time.Date(2026, 6, 3, 9, 5, 0, 0, time.UTC)
	store := newGatewayMemoryAuthStore(now)
	authSvc := userauth.NewService(store)
	authSvc.PasswordPolicy = userauth.PasswordPolicy{
		MemoryKiB: 64, Iterations: 1, Parallelism: 1,
		SaltBytes: 8, KeyBytes: 16,
	}
	authSvc.Now = func() time.Time { return now }
	gate := &authCaptchaStub{}
	r := chi.NewRouter()
	r.Route("/v1/auth", func(r chi.Router) {
		MountAuthRoutes(r, AuthHandlerDeps{Auth: authSvc, Captcha: gate})
	})

	rec := serveJSON(t, r, http.MethodPost, "/v1/auth/register", map[string]any{
		"tenant_id": 1, "email": "human@example.test", "password": "secret",
		"captcha_token": "valid-token",
	})

	assertHTTPStatus(t, rec, http.StatusCreated)
	if got := gate.calls(); got != 1 {
		t.Fatalf("captcha calls = %d want 1", got)
	}
	if got := gate.lastToken(); got != "valid-token" {
		t.Fatalf("captcha token = %q want valid-token", got)
	}
	_, err := store.GetUserByEmail(context.Background(), 1, "human@example.test")
	if err != nil {
		t.Fatalf("captcha success should create user: %v", err)
	}
}

func TestAuthLogin_CaptchaFailureRejectsBeforeAuthenticate(t *testing.T) {
	now := time.Date(2026, 6, 3, 9, 10, 0, 0, time.UTC)
	base := newGatewayMemoryAuthStore(now)
	seedLoginUser(
		t, base, "login@example.test", "secret",
		userauth.UserStatusActive, true,
	)
	counting := &gatewayCountingAuthStore{gatewayMemoryAuthStore: base}
	r := newAuthCaptchaLoginRouter(
		t, now, counting,
		&authCaptchaStub{err: captcha.ErrVerificationFailed},
	)

	rec := serveJSON(t, r, http.MethodPost, "/v1/auth/login", map[string]any{
		"tenant_id": 1, "email": "login@example.test", "password": "secret",
	})

	assertHTTPStatus(t, rec, http.StatusForbidden)
	if code := loginErrorCode(t, rec); code != "captcha_required" {
		t.Fatalf("error code = %q want captcha_required", code)
	}
	if got := counting.calls(); got != 0 {
		t.Fatalf("captcha failure must not reach Authenticate, lookups=%d", got)
	}
}

func TestAuthLogin_CaptchaSuccessAllowsSessionCreate(t *testing.T) {
	now := time.Date(2026, 6, 3, 9, 15, 0, 0, time.UTC)
	base := newGatewayMemoryAuthStore(now)
	seedLoginUser(
		t, base, "login-ok@example.test", "secret",
		userauth.UserStatusActive, true,
	)
	gate := &authCaptchaStub{}
	r := newAuthCaptchaLoginRouter(t, now, base, gate)

	rec := serveJSON(t, r, http.MethodPost, "/v1/auth/login", map[string]any{
		"tenant_id": 1, "email": "login-ok@example.test", "password": "secret",
		"captcha_token": "valid-token",
	})

	assertHTTPStatus(t, rec, http.StatusOK)
	if got := gate.calls(); got != 1 {
		t.Fatalf("captcha calls = %d want 1", got)
	}
	if got := gate.lastToken(); got != "valid-token" {
		t.Fatalf("captcha token = %q want valid-token", got)
	}
}

func TestAuthLogin_DefaultTwoFactorSettingRequiresOptedInUsersOnly(t *testing.T) {
	now := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)

	t.Run("unbound user keeps password login unchanged", func(t *testing.T) {
		base := newGatewayMemoryAuthStore(now)
		seedLoginUser(t, base, "default-unbound@example.test", "secret", userauth.UserStatusActive, true)
		user := mustGatewayUserByEmail(t, base, "default-unbound@example.test")
		sessionSvc := newGatewayTestSessionService(now)
		settings := platformsettings.NewService(platformsettings.NewMemoryStore(), nil)
		twoFA := twofa.NewService(twofa.NewMemoryStore(), mustGatewayTwoFAKeyProvider(t), twofa.WithNow(func() time.Time { return now }))
		events := &captureAuthEventSink{}
		r := newTwoFALoginTestRouter(t, now, base, sessionSvc, twoFA, settings, events)

		rec := serveJSON(t, r, http.MethodPost, "/v1/auth/login", map[string]any{
			"tenant_id": 1, "email": "default-unbound@example.test", "password": "secret",
		})
		assertHTTPStatus(t, rec, http.StatusOK)
		body := rec.Body.String()
		if strings.Contains(body, "two_factor_required") {
			t.Fatalf("unbound default login unexpectedly required 2FA: %s", body)
		}
		var loginResp struct {
			Session usersession.IssuedTokens `json:"session"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &loginResp); err != nil {
			t.Fatalf("decode login response: %v body=%s", err, body)
		}
		if loginResp.Session.RefreshToken == "" {
			t.Fatal("unbound default password login did not issue a session")
		}
		assertGatewaySessionCount(t, sessionSvc, user.TenantID, user.ID, 1)
	})

	t.Run("bound user gets challenge before session", func(t *testing.T) {
		base := newGatewayMemoryAuthStore(now)
		seedLoginUser(t, base, "default-bound@example.test", "secret", userauth.UserStatusActive, true)
		user := mustGatewayUserByEmail(t, base, "default-bound@example.test")
		sessionSvc := newGatewayTestSessionService(now)
		settings := platformsettings.NewService(platformsettings.NewMemoryStore(), nil)
		twoFA := twofa.NewService(twofa.NewMemoryStore(), mustGatewayTwoFAKeyProvider(t), twofa.WithNow(func() time.Time { return now }))
		enableGatewayTwoFA(t, twoFA, user.TenantID, user.ID, "default-bound@example.test", now)
		events := &captureAuthEventSink{}
		r := newTwoFALoginTestRouter(t, now, base, sessionSvc, twoFA, settings, events)

		rec := serveJSON(t, r, http.MethodPost, "/v1/auth/login", map[string]any{
			"tenant_id": 1, "email": "default-bound@example.test", "password": "secret",
		})
		assertHTTPStatus(t, rec, http.StatusAccepted)
		challenge := decodeGatewayTwoFAChallenge(t, rec)
		if !challenge.TwoFactorRequired || challenge.ChallengeID == "" {
			t.Fatalf("bound default login did not return 2FA challenge: %+v body=%s", challenge, rec.Body.String())
		}
		if challenge.Session != nil {
			t.Fatalf("bound default login must not issue session before 2FA: %+v", challenge.Session)
		}
		assertGatewaySessionCount(t, sessionSvc, user.TenantID, user.ID, 0)
	})
}

func TestAuthLogin_TwoFactorKillSwitchLetsBoundUserLoginWithoutChallenge(t *testing.T) {
	now := time.Date(2026, 6, 3, 10, 5, 0, 0, time.UTC)
	base := newGatewayMemoryAuthStore(now)
	seedLoginUser(t, base, "kill-switch@example.test", "secret", userauth.UserStatusActive, true)
	user := mustGatewayUserByEmail(t, base, "kill-switch@example.test")
	sessionSvc := newGatewayTestSessionService(now)
	settings := platformsettings.NewService(platformsettings.NewMemoryStore(), nil)
	if _, err := settings.Upsert(context.Background(), platformsettings.UpsertInput{
		Key: platformsettings.KeyTwoFactorEnabled, Value: "false", UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("disable platform 2FA setting: %v", err)
	}
	twoFA := twofa.NewService(twofa.NewMemoryStore(), mustGatewayTwoFAKeyProvider(t), twofa.WithNow(func() time.Time { return now }))
	enableGatewayTwoFA(t, twoFA, user.TenantID, user.ID, "kill-switch@example.test", now)
	events := &captureAuthEventSink{}
	r := newTwoFALoginTestRouter(t, now, base, sessionSvc, twoFA, settings, events)

	rec := serveJSON(t, r, http.MethodPost, "/v1/auth/login", map[string]any{
		"tenant_id": 1, "email": "kill-switch@example.test", "password": "secret",
	})
	assertHTTPStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	if strings.Contains(body, "two_factor_required") {
		t.Fatalf("kill-switch login unexpectedly required 2FA: %s", body)
	}
	var loginResp struct {
		Session usersession.IssuedTokens `json:"session"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("decode login response: %v body=%s", err, body)
	}
	if loginResp.Session.RefreshToken == "" {
		t.Fatal("kill-switch password login did not issue a session")
	}
	assertGatewaySessionCount(t, sessionSvc, user.TenantID, user.ID, 1)
}

func TestAuthLogin_TwoFactorEnabledRequiresChallengeBeforeSession(t *testing.T) {
	now := time.Date(2026, 6, 3, 10, 15, 0, 0, time.UTC)
	base := newGatewayMemoryAuthStore(now)
	seedLoginUser(t, base, "mfa@example.test", "secret", userauth.UserStatusActive, true)
	user := mustGatewayUserByEmail(t, base, "mfa@example.test")
	sessionSvc := newGatewayTestSessionService(now)
	settings := enabledTwoFAPlatformSettings(t)
	twoFA := twofa.NewService(twofa.NewMemoryStore(), mustGatewayTwoFAKeyProvider(t), twofa.WithNow(func() time.Time { return now }))
	setup := enableGatewayTwoFA(t, twoFA, user.TenantID, user.ID, "mfa@example.test", now)
	events := &captureAuthEventSink{}
	r := newTwoFALoginTestRouter(t, now, base, sessionSvc, twoFA, settings, events)

	rec := serveJSON(t, r, http.MethodPost, "/v1/auth/login", map[string]any{
		"tenant_id": 1, "email": "mfa@example.test", "password": "secret",
	})
	assertHTTPStatus(t, rec, http.StatusAccepted)
	firstChallenge := decodeGatewayTwoFAChallenge(t, rec)
	if !firstChallenge.TwoFactorRequired || firstChallenge.ChallengeID == "" {
		t.Fatalf("password login did not return a 2FA challenge: %+v body=%s", firstChallenge, rec.Body.String())
	}
	if firstChallenge.Session != nil {
		t.Fatalf("password login must not issue session before 2FA: %+v", firstChallenge.Session)
	}
	assertGatewaySessionCount(t, sessionSvc, user.TenantID, user.ID, 0)

	rec = serveJSON(t, r, http.MethodPost, "/v1/auth/login/2fa", map[string]any{
		"challenge_id": firstChallenge.ChallengeID,
		"code":         "000000",
	})
	assertHTTPStatus(t, rec, http.StatusUnauthorized)
	if code := loginErrorCode(t, rec); code != "two_factor_invalid" {
		t.Fatalf("wrong 2FA error code=%q want two_factor_invalid", code)
	}
	assertGatewaySessionCount(t, sessionSvc, user.TenantID, user.ID, 0)

	rec = serveJSON(t, r, http.MethodPost, "/v1/auth/login/2fa", map[string]any{
		"challenge_id": firstChallenge.ChallengeID,
		"code":         setup.BackupCodes[0],
	})
	assertHTTPStatus(t, rec, http.StatusOK)
	var loginResp struct {
		Session usersession.IssuedTokens `json:"session"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("decode 2FA login response: %v body=%s", err, rec.Body.String())
	}
	if loginResp.Session.RefreshToken == "" {
		t.Fatal("2FA login did not issue a session after valid backup code")
	}
	assertGatewaySessionCount(t, sessionSvc, user.TenantID, user.ID, 1)

	rec = serveJSON(t, r, http.MethodPost, "/v1/auth/login", map[string]any{
		"tenant_id": 1, "email": "mfa@example.test", "password": "secret",
	})
	assertHTTPStatus(t, rec, http.StatusAccepted)
	secondChallenge := decodeGatewayTwoFAChallenge(t, rec)
	rec = serveJSON(t, r, http.MethodPost, "/v1/auth/login/2fa", map[string]any{
		"challenge_id": secondChallenge.ChallengeID,
		"code":         setup.BackupCodes[0],
	})
	assertHTTPStatus(t, rec, http.StatusUnauthorized)
	if code := loginErrorCode(t, rec); code != "two_factor_invalid" {
		t.Fatalf("reused backup code error code=%q want two_factor_invalid", code)
	}
	assertGatewaySessionCount(t, sessionSvc, user.TenantID, user.ID, 1)

	if reason := lastAuthEventType(t, events); reason != "user_login_2fa_failed" {
		t.Fatalf("last auth event=%q want user_login_2fa_failed", reason)
	}
}

func TestAuthLogin_TwoFactorMaterialsAreNotLoggedOrReturned(t *testing.T) {
	now := time.Date(2026, 6, 3, 10, 30, 0, 0, time.UTC)
	base := newGatewayMemoryAuthStore(now)
	seedLoginUser(t, base, "no-leak@example.test", "secret", userauth.UserStatusActive, true)
	user := mustGatewayUserByEmail(t, base, "no-leak@example.test")
	sessionSvc := newGatewayTestSessionService(now)
	settings := enabledTwoFAPlatformSettings(t)
	twoFA := twofa.NewService(twofa.NewMemoryStore(), mustGatewayTwoFAKeyProvider(t), twofa.WithNow(func() time.Time { return now }))
	setup := enableGatewayTwoFA(t, twoFA, user.TenantID, user.ID, "no-leak@example.test", now)
	events := &captureAuthEventSink{}
	r := newTwoFALoginTestRouter(t, now, base, sessionSvc, twoFA, settings, events)

	var systemLog bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&systemLog, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	scannedResponses := map[string]any{}
	rec := serveJSON(t, r, http.MethodPost, "/v1/auth/login", map[string]any{
		"tenant_id": 1, "email": "no-leak@example.test", "password": "secret",
	})
	assertHTTPStatus(t, rec, http.StatusAccepted)
	scannedResponses["password_login_challenge"] = rec.Body.String()
	challenge := decodeGatewayTwoFAChallenge(t, rec)

	rec = serveJSON(t, r, http.MethodPost, "/v1/auth/login/2fa", map[string]any{
		"challenge_id": challenge.ChallengeID,
		"code":         "000000",
	})
	assertHTTPStatus(t, rec, http.StatusUnauthorized)
	scannedResponses["wrong_2fa_response"] = rec.Body.String()

	sentinels := append([]string{
		setup.Secret,
		gatewayTOTPCode(t, setup.Secret, now),
	}, setup.BackupCodes...)
	assertSentinelsAbsent(t, scannedResponses, sentinels)
	assertSentinelsAbsent(t, map[string]any{
		"system_logger_output": systemLog.String(),
		"auth_event_sinks":     events.SinkPayloads(),
	}, sentinels)
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
	stateCookie := findOAuthStateCookie(t, rec.Result().Cookies(), initResp.State)
	rec = serveJSONWithCookies(t, allowedRouter, http.MethodPost, "/v1/auth/oauth-callback", map[string]any{
		"tenant_id": 1, "provider": userauth.SocialProviderGoogle, "state": initResp.State, "code": "provider-code",
	}, []*http.Cookie{stateCookie})
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

// 变异:防护测。删掉 callback 的 cookie-state 校验会让不匹配的 callback
// 也能换取 provider code; 省掉清理则会让 state cookie 在一次成功 callback 后
// 仍可复用。
func TestOAuthCallbackRequiresStateCookieBeforeProviderExchange(t *testing.T) {
	now := time.Date(2026, 6, 4, 14, 0, 0, 0, time.UTC)
	authStore := newGatewayMemoryAuthStore(now)
	authSvc := userauth.NewService(authStore)
	provider := &gatewayFakeOAuthProvider{
		provider: userauth.SocialProviderGoogle,
		identity: userauth.VerifiedIdentity{
			Provider: userauth.SocialProviderGoogle, Subject: "google-state-cookie-subject",
			Email: "state-cookie@example.test", DisplayName: "State Cookie", EmailVerified: true,
		},
	}
	authSvc.OAuth = userauth.NewOAuthService(provider)
	sessionSvc := usersession.NewService(usersession.NewMemoryStore())
	sessionSvc.Now = func() time.Time { return now }
	sessionSvc.SigningKey = testSessionSigningKey()

	router := chi.NewRouter()
	router.Route("/v1/auth", func(r chi.Router) {
		MountAuthRoutes(r, AuthHandlerDeps{Auth: authSvc, Sessions: sessionSvc})
	})

	initRec := serveJSON(t, router, http.MethodPost, "/v1/auth/oauth-init", map[string]any{
		"tenant_id": 1, "provider": userauth.SocialProviderGoogle,
	})
	assertHTTPStatus(t, initRec, http.StatusCreated)
	var initResp userauth.OAuthInitResult
	if err := json.Unmarshal(initRec.Body.Bytes(), &initResp); err != nil {
		t.Fatalf("decode oauth init: %v", err)
	}
	stateCookie := findOAuthStateCookie(t, initRec.Result().Cookies(), initResp.State)
	if !stateCookie.HttpOnly || !stateCookie.Secure ||
		stateCookie.SameSite != http.SameSiteLaxMode ||
		stateCookie.Path != "/v1/auth/oauth-callback" ||
		stateCookie.MaxAge < 590 || stateCookie.MaxAge > 600 {
		t.Fatalf("oauth state cookie attributes = %+v", stateCookie)
	}

	badRec := serveJSONWithCookies(t, router, http.MethodPost, "/v1/auth/oauth-callback", map[string]any{
		"tenant_id": 1, "provider": userauth.SocialProviderGoogle,
		"state": initResp.State, "code": "provider-code",
	}, []*http.Cookie{{
		Name: stateCookie.Name, Value: "attacker-state", Path: stateCookie.Path,
	}})
	assertHTTPStatus(t, badRec, http.StatusForbidden)
	if provider.exchanges != 0 {
		t.Fatalf("mismatched cookie exchanged provider code %d times; want 0", provider.exchanges)
	}

	okRec := serveJSONWithCookies(t, router, http.MethodPost, "/v1/auth/oauth-callback", map[string]any{
		"tenant_id": 1, "provider": userauth.SocialProviderGoogle,
		"state": initResp.State, "code": "provider-code",
	}, []*http.Cookie{stateCookie})
	assertHTTPStatus(t, okRec, http.StatusOK)
	if provider.exchanges != 1 {
		t.Fatalf("matched cookie exchanged provider code %d times; want 1", provider.exchanges)
	}
	clearCookie := findCookieByName(t, okRec.Result().Cookies(), stateCookie.Name)
	if clearCookie.Path != stateCookie.Path || clearCookie.MaxAge >= 0 {
		t.Fatalf("oauth state cookie cleanup = %+v, want same path and MaxAge < 0", clearCookie)
	}
}

func TestSafeSocialProviderAcceptsMultiOAuthProviders(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"QQ", userauth.SocialProviderQQ},
		{" WeChat ", userauth.SocialProviderWeChat},
		{"DINGTALK", userauth.SocialProviderDingTalk},
		{"nodeseek", userauth.SocialProviderNodeSeek},
		{"linuxdo", userauth.SocialProviderLinuxDo},
		{"OIDC", userauth.SocialProviderOIDC},
		{"discord", userauth.SocialProviderDiscord},
		{"TELEGRAM", userauth.SocialProviderTelegram},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			got, ok := safeSocialProvider(tc.in)
			if !ok || got != tc.want {
				t.Fatalf("safeSocialProvider(%q) = (%q, %v), want (%q, true)", tc.in, got, ok, tc.want)
			}
		})
	}
}

func TestTelegramLoginWidgetRejectsEmaillessIdentityWithPendingEmail(t *testing.T) {
	now := time.Date(2026, 6, 7, 10, 30, 0, 0, time.UTC)
	authStore := newGatewayMemoryAuthStore(now)
	authSvc := userauth.NewService(authStore)
	authSvc.Now = func() time.Time { return now }
	sessions := newGatewayTestSessionService(now)
	router := chi.NewRouter()
	router.Route("/v1/auth", func(r chi.Router) {
		MountAuthRoutes(r, AuthHandlerDeps{
			Auth: authSvc, Sessions: sessions,
			TelegramBotToken:     "123456:bot-secret",
			TelegramWidgetMaxAge: 24 * time.Hour,
		})
	})

	params := signedTelegramGatewayParams("123456:bot-secret", map[string]string{
		"id":         "424242",
		"first_name": "Ada",
		"last_name":  "Lovelace",
		"username":   "ada_dev",
		"auth_date":  strconv.FormatInt(now.Unix(), 10),
	})
	rec := serveJSON(t, router, http.MethodPost, "/v1/auth/telegram-login", map[string]any{
		"tenant_id":   1,
		"params":      params,
		"device_info": map[string]any{"device": "browser"},
	})
	assertHTTPStatus(t, rec, http.StatusAccepted)
	if !strings.Contains(rec.Body.String(), "oauth_pending_email_required") {
		t.Fatalf("Telegram pending-email body=%s", rec.Body.String())
	}
	if len(authStore.users) != 0 || len(authStore.socialLinks) != 0 {
		t.Fatalf("Telegram pending-email handler persisted users=%+v links=%+v", authStore.users, authStore.socialLinks)
	}
}

func TestAuthReasonClassForPendingOAuth(t *testing.T) {
	if got := authReasonClass(userauth.ErrOAuthPendingEmailRequired); got != "oauth_pending_email_required" {
		t.Fatalf("authReasonClass(ErrOAuthPendingEmailRequired) = %q, want oauth_pending_email_required", got)
	}
}

func TestWriteAuthErrorForPendingOAuthAvoidsBackendError(t *testing.T) {
	rec := httptest.NewRecorder()
	writeAuthError(rec, userauth.ErrOAuthPendingEmailRequired)
	assertHTTPStatus(t, rec, http.StatusAccepted)
	if strings.Contains(rec.Body.String(), "auth_backend_error") ||
		!strings.Contains(rec.Body.String(), "oauth_pending_email_required") {
		t.Fatalf("pending OAuth error body = %s, want pending reason and no backend fallback", rec.Body.String())
	}
}

func signedTelegramGatewayParams(botToken string, params map[string]string) map[string]string {
	out := make(map[string]string, len(params)+1)
	for k, v := range params {
		out[k] = v
	}
	secret := sha256.Sum256([]byte(botToken))
	mac := hmac.New(sha256.New, secret[:])
	mac.Write([]byte(telegramGatewayCheckString(out)))
	out["hash"] = hex.EncodeToString(mac.Sum(nil))
	return out
}

func telegramGatewayCheckString(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		if key != "hash" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+"="+params[key])
	}
	return strings.Join(lines, "\n")
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

func serveJSONWithCookies(t *testing.T, h http.Handler, method, target string, body any, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func findOAuthStateCookie(t *testing.T, cookies []*http.Cookie, state string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Value == state {
			return cookie
		}
	}
	t.Fatalf("oauth init did not set state cookie matching returned state; cookies=%+v", cookies)
	return nil
}

func findCookieByName(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("response did not set cookie %q; cookies=%+v", name, cookies)
	return nil
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
	provider  string
	identity  userauth.VerifiedIdentity
	exchanges int
}

func (p *gatewayFakeOAuthProvider) Provider() string { return p.provider }

func (p *gatewayFakeOAuthProvider) AuthorizationURL(challenge userauth.OAuthFlowChallenge) (string, error) {
	return "https://auth.example.test/authorize?state=" + challenge.State, nil
}

func (p *gatewayFakeOAuthProvider) ExchangeVerifiedIdentity(_ context.Context, flow userauth.OAuthFlowSession, code string) (userauth.VerifiedIdentity, error) {
	p.exchanges++
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

type authCaptchaStub struct {
	mu     sync.Mutex
	err    error
	tokens []string
	callsN int64
}

func (s *authCaptchaStub) Verify(_ context.Context, token, _ string) error {
	atomic.AddInt64(&s.callsN, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens = append(s.tokens, token)
	return s.err
}

func (s *authCaptchaStub) calls() int64 {
	return atomic.LoadInt64(&s.callsN)
}

func (s *authCaptchaStub) lastToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.tokens) == 0 {
		return ""
	}
	return s.tokens[len(s.tokens)-1]
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
func (s *gatewayMemoryAuthStore) ClearLockout(_ context.Context, tenantID, userID int64) (userauth.User, error) {
	user, ok := s.users[userID]
	if !ok || user.TenantID != tenantID {
		return userauth.User{}, userauth.ErrUserNotFound
	}
	user.FailedLoginCount = 0
	if user.Status == userauth.UserStatusLocked {
		user.Status = userauth.UserStatusActive
	}
	s.users[userID] = user
	return user, nil
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

func (s *gatewayMemoryAuthStore) CreateCommunityReferral(context.Context, int64, int64, int64, int64) error {
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

type gatewayTwoFAChallengeResponse struct {
	TwoFactorRequired  bool                      `json:"two_factor_required"`
	ChallengeID        string                    `json:"challenge_id"`
	ChallengeExpiresAt string                    `json:"challenge_expires_at"`
	Session            *usersession.IssuedTokens `json:"session"`
}

func newGatewayTestSessionService(now time.Time) *usersession.Service {
	sessionSvc := usersession.NewService(usersession.NewMemoryStore())
	sessionSvc.Now = func() time.Time { return now }
	sessionSvc.SigningKey = testSessionSigningKey()
	return sessionSvc
}

func mustGatewayTwoFAKeyProvider(t *testing.T) credentialstore.KeyProvider {
	t.Helper()
	provider, err := credentialstore.NewStaticKeyProvider("twofa-gateway-test-key", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("NewStaticKeyProvider: %v", err)
	}
	return provider
}

func enabledTwoFAPlatformSettings(t *testing.T) *platformsettings.Service {
	t.Helper()
	settings := platformsettings.NewService(platformsettings.NewMemoryStore(), nil)
	if _, err := settings.Upsert(context.Background(), platformsettings.UpsertInput{
		Key: platformsettings.KeyTwoFactorEnabled, Value: "true", UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("enable platform 2FA setting: %v", err)
	}
	return settings
}

func mustGatewayUserByEmail(t *testing.T, store *gatewayMemoryAuthStore, email string) userauth.User {
	t.Helper()
	user, err := store.GetUserByEmail(context.Background(), 1, email)
	if err != nil {
		t.Fatalf("GetUserByEmail(%q): %v", email, err)
	}
	return user
}

func enableGatewayTwoFA(t *testing.T, service *twofa.Service, tenantID, userID int64, accountName string, now time.Time) twofa.SetupResult {
	t.Helper()
	setup, err := service.Setup(context.Background(), twofa.SetupInput{
		TenantID: tenantID, UserID: userID, AccountName: accountName,
	})
	if err != nil {
		t.Fatalf("2FA Setup: %v", err)
	}
	if _, err := service.Enable(context.Background(), twofa.VerifyInput{
		TenantID: tenantID, UserID: userID, Code: gatewayTOTPCode(t, setup.Secret, now),
	}); err != nil {
		t.Fatalf("2FA Enable: %v", err)
	}
	return setup
}

func gatewayTOTPCode(t *testing.T, encoded string, now time.Time) string {
	t.Helper()
	secret, err := twofa.DecodeSecret(encoded)
	if err != nil {
		t.Fatalf("DecodeSecret: %v", err)
	}
	code, err := twofa.GenerateTOTP(secret, now, twofa.DefaultTOTPDigits, twofa.DefaultTOTPStep)
	if err != nil {
		t.Fatalf("GenerateTOTP: %v", err)
	}
	return code
}

func newTwoFALoginTestRouter(
	t *testing.T,
	now time.Time,
	store userauth.Store,
	sessionSvc *usersession.Service,
	twoFA *twofa.Service,
	settings *platformsettings.Service,
	events *captureAuthEventSink,
) http.Handler {
	t.Helper()
	authSvc := userauth.NewService(store)
	authSvc.PasswordPolicy = userauth.PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	authSvc.Now = func() time.Time { return now }
	resolver, err := clientip.NewResolver(nil)
	if err != nil {
		t.Fatalf("client ip resolver: %v", err)
	}
	r := chi.NewRouter()
	r.Route("/v1/auth", func(r chi.Router) {
		MountAuthRoutes(r, AuthHandlerDeps{
			Auth: authSvc, Sessions: sessionSvc, EventSink: events,
			ClientIPResolver: resolver, TwoFactor: twoFA, TwoFactorSettings: settings,
		})
	})
	return r
}

func decodeGatewayTwoFAChallenge(t *testing.T, rec *httptest.ResponseRecorder) gatewayTwoFAChallengeResponse {
	t.Helper()
	var resp gatewayTwoFAChallengeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode 2FA challenge: %v body=%s", err, rec.Body.String())
	}
	return resp
}

func assertGatewaySessionCount(t *testing.T, sessions *usersession.Service, tenantID, userID int64, want int) {
	t.Helper()
	families, err := sessions.List(context.Background(), tenantID, userID)
	if err != nil {
		t.Fatalf("List sessions: %v", err)
	}
	if len(families) != want {
		t.Fatalf("session count=%d want %d families=%+v", len(families), want, families)
	}
}

func lastAuthEventType(t *testing.T, events *captureAuthEventSink) string {
	t.Helper()
	events.mu.Lock()
	defer events.mu.Unlock()
	if len(events.events) == 0 {
		t.Fatal("no auth events recorded")
	}
	return events.events[len(events.events)-1].EventType
}

// flakyRevokeSessionStore 内嵌一个可用的内存 store, 但强制第一次 user 范围的 revoke
// 失败, 以驱动「改密前 revoke 失败」这条路径, 同时证明在 session 后端恢复后, 同一个
// reset token 仍可重试。
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

// resetConfirmTestSetup 注册并验证一个用户, 返回一个真实可用的 router 以及捕获到的
// reset token, 这样 reset-confirm 测试就能走到真实的 handler 路径。
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

// TestAuthPasswordResetConfirmFailsClosedWhenRevokeFails 钉住: reset-confirm 必须先把
// reset 的目标账号置于登录屏障之后, 然后在旧 session 被先行 revoke 之前, 拒绝消费一次性
// token、也拒绝改密。注入的 store 只让第一次 RevokeUser 失败, 接着在第一次成功 revoke 之后
// 创建一个新的活跃 family, 模拟一次进行中的旧口令登录。该 fixture 用以区分:带提交后清扫
// 的正确 fail-closed/可重试 handler、错误的「改密后尽力 revoke」路径, 以及在等待重试时仍
// 开放旧口令登录的「revoke 前」实现。
//
// 变异:检查。把 ResetPassword 移到 Revoke 之前、忽略 Revoke 的 error、移除 reset 登录屏障,
// 或移除提交后的 revoke 清扫;则要么第一次响应变成 2xx、要么新口令过早可认证、要么重试前
// 旧口令登录仍可用、要么注入的竞态 family 在重试后仍活跃 -> 变红。
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

// TestAuthPasswordResetConfirmPostSweepFailureIsDegradedSuccess 钉住「token 已提交」这一半:
// 一旦 ResetPassword 已经改了口令并消费了一次性 token, 之后的提交后清扫失败必须被报告为
// 降级的 reset 成功, 而不是可重试的 5xx。
//
// 变异:检查。在 ResetPassword 之后返回 writeSessionError, 或始终报告 "revoked";则
// status/session_revocation 断言变红, 而新口令证明 token 已被消费。
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

// TestAuthPasswordResetConfirmRequiresSessions 钉住: 当没有接入 session store 时,
// reset-confirm 必须拒绝(它无法 revoke session)且绝不能改密 —— 该防护在 ResetPassword
// 之前执行。旧代码在 Sessions==nil 时跳过 revoke, 却仍然改了密并以 SessionPolicy "revoked"
// 报告成功。
//
// 变异:检查。移除 `if d.Sessions == nil` 防护;口令于是被改(旧口令失效)→「旧口令仍可
// 认证」断言变红。
func TestAuthPasswordResetConfirmRequiresSessions(t *testing.T) {
	r, email, authSvc := resetConfirmTestSetup(t, nil) // 未接入 session store

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
	// 真正的判别点: 口令必须保持不变(防护在 ResetPassword 之前已执行)。
	if _, err := authSvc.Authenticate(context.Background(), userauth.LoginInput{TenantID: 1, Email: "reset@example.test", Password: "secret-old"}); err != nil {
		t.Fatalf("old password must still authenticate — reset must not change the password when sessions cannot be revoked; got %v", err)
	}
}

// TestAuthPasswordResetConfirmRefusesWhenSessionStoreUnset 钉住: 一个非 nil 的 session
// Service, 但其底层 Store 未设置(NewService(nil)), 仍必须在口令被改之前被拒 —— 仅检查
// service 指针是不够的, 因为那样 Revoke 只会在一次性 token 已被消费、口令已被改之后才以
// ErrStoreNotConfigured 失败。
//
// 变异:检查。从防护中去掉 `|| d.Sessions.Store == nil` 子句;reset 继续执行
// (旧口令失效)→「旧口令仍可认证」断言变红。
func TestAuthPasswordResetConfirmRefusesWhenSessionStoreUnset(t *testing.T) {
	r, email, authSvc := resetConfirmTestSetup(t, usersession.NewService(nil)) // service 已设置, 底层 store 未设置

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

// gatewayCountingAuthStore 包住内存 store 并数 GetUserByEmail 次数, 用于证明限流命中时登录请求
// 根本没走到查用户 / argon2(pre-KDF 顺序)。
type gatewayCountingAuthStore struct {
	*gatewayMemoryAuthStore
	getByEmail int64
}

func (s *gatewayCountingAuthStore) GetUserByEmail(ctx context.Context, tenantID int64, email string) (userauth.User, error) {
	atomic.AddInt64(&s.getByEmail, 1)
	return s.gatewayMemoryAuthStore.GetUserByEmail(ctx, tenantID, email)
}

func (s *gatewayCountingAuthStore) calls() int64 { return atomic.LoadInt64(&s.getByEmail) }

func seedLoginUser(t *testing.T, store *gatewayMemoryAuthStore, email, password string, status userauth.UserStatus, emailVerified bool) {
	t.Helper()
	cheap := userauth.PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	hash, err := userauth.HashPassword(password, cheap)
	if err != nil {
		t.Fatalf("hash seed password: %v", err)
	}
	if _, err := store.CreateUser(context.Background(), userauth.CreateUserParams{
		TenantID: 1, Email: email, PasswordHash: hash, EmailVerified: emailVerified, Status: status,
	}); err != nil {
		t.Fatalf("seed login user: %v", err)
	}
}

func newLoginTestHandler(t *testing.T, now time.Time, store userauth.Store, throttle *loginthrottle.Limiter, requireVerified bool) (http.Handler, *captureAuthEventSink) {
	t.Helper()
	authSvc := userauth.NewService(store)
	authSvc.PasswordPolicy = userauth.PasswordPolicy{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltBytes: 8, KeyBytes: 16}
	authSvc.RequireVerified = requireVerified
	authSvc.Now = func() time.Time { return now }
	sessionSvc := usersession.NewService(usersession.NewMemoryStore())
	sessionSvc.Now = func() time.Time { return now }
	sessionSvc.SigningKey = testSessionSigningKey()
	resolver, err := clientip.NewResolver(nil)
	if err != nil {
		t.Fatalf("client ip resolver: %v", err)
	}
	events := &captureAuthEventSink{}
	r := chi.NewRouter()
	r.Route("/v1/auth", func(r chi.Router) {
		MountAuthRoutes(r, AuthHandlerDeps{
			Auth: authSvc, Sessions: sessionSvc, EventSink: events,
			ClientIPResolver: resolver, LoginThrottle: throttle,
		})
	})
	return r, events
}

func newAuthCaptchaLoginRouter(
	t *testing.T,
	now time.Time,
	store userauth.Store,
	gate *authCaptchaStub,
) http.Handler {
	t.Helper()
	authSvc := userauth.NewService(store)
	authSvc.PasswordPolicy = userauth.PasswordPolicy{
		MemoryKiB: 64, Iterations: 1, Parallelism: 1,
		SaltBytes: 8, KeyBytes: 16,
	}
	authSvc.Now = func() time.Time { return now }
	sessionSvc := usersession.NewService(usersession.NewMemoryStore())
	sessionSvc.Now = func() time.Time { return now }
	sessionSvc.SigningKey = testSessionSigningKey()
	resolver, err := clientip.NewResolver(nil)
	if err != nil {
		t.Fatalf("client ip resolver: %v", err)
	}
	r := chi.NewRouter()
	r.Route("/v1/auth", func(r chi.Router) {
		MountAuthRoutes(r, AuthHandlerDeps{
			Auth: authSvc, Sessions: sessionSvc,
			ClientIPResolver: resolver, Captcha: gate,
		})
	})
	return r
}

func loginErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var resp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error body: %v (%s)", err, rec.Body.String())
	}
	return resp.Error.Code
}

func lastLoginFailedReason(t *testing.T, events *captureAuthEventSink) string {
	t.Helper()
	events.mu.Lock()
	defer events.mu.Unlock()
	for i := len(events.events) - 1; i >= 0; i-- {
		if events.events[i].EventType == "user_login_failed" {
			return events.events[i].ReasonClass
		}
	}
	t.Fatal("no user_login_failed audit event was recorded")
	return ""
}

// TestLogin_ThrottleBlocksBeforeKDF 是 门1 的核心判别测: 限流命中时, 登录请求必须在调用
// Authenticate(查用户 + argon2)之前就被 429 挡掉。用「查用户次数」证明 pre-KDF 顺序: 被限流的那
// 次请求绝不能再触发一次 GetUserByEmail(进而 argon2)。
//
// mutation: 把 handler 里的限流闸移到 Authenticate 之后 → 被限流的请求仍先查了用户/跑了 argon2 →
// 查用户次数从 1 涨到 2 → 红(未认证 CPU 放大 DoS 复活)。
func TestLogin_ThrottleBlocksBeforeKDF(t *testing.T) {
	now := time.Date(2026, 5, 31, 9, 0, 0, 0, time.UTC)
	base := newGatewayMemoryAuthStore(now)
	seedLoginUser(t, base, "u@example.test", "secret", userauth.UserStatusActive, true)
	counting := &gatewayCountingAuthStore{gatewayMemoryAuthStore: base}
	// WindowLimit=1: 第一次失败后, 同 IP 第二次在限流闸即被拒(不进 Authenticate)。
	limiter := loginthrottle.New(loginthrottle.Config{WindowLimit: 1, InFlightLimit: 10, BanAfter: 100, Now: func() time.Time { return now }})
	r, _ := newLoginTestHandler(t, now, counting, limiter, false)

	body := map[string]any{"tenant_id": 1, "email": "u@example.test", "password": "wrong"}
	rec1 := serveJSON(t, r, http.MethodPost, "/v1/auth/login", body)
	assertHTTPStatus(t, rec1, http.StatusUnauthorized)
	if code := loginErrorCode(t, rec1); code != "invalid_credentials" {
		t.Fatalf("wrong-password code=%q, want generic invalid_credentials", code)
	}
	hits := counting.calls()
	if hits != 1 {
		t.Fatalf("first login should reach the store exactly once, got %d", hits)
	}

	rec2 := serveJSON(t, r, http.MethodPost, "/v1/auth/login", body)
	assertHTTPStatus(t, rec2, http.StatusTooManyRequests)
	if got := counting.calls(); got != hits {
		t.Fatalf("throttled login MUST NOT reach the store/argon2 (pre-KDF); store lookups went %d -> %d", hits, got)
	}
	if rec2.Header().Get("Retry-After") == "" {
		t.Fatal("429 throttle response must carry a Retry-After header")
	}
}

// TestLogin_AccountStateFailuresAreGeneric 是 门2 的判别测: 所有「账号存在性/状态」相关的
// 登录失败对外必须是同一个 generic 401 invalid_credentials(消状态码枚举 oracle), 但审计事件仍保留
// 真实 reason_class(操作员可见)。
//
// mutation: 任一状态在 handler 仍走 writeAuthError(保留 403 user_disabled 等专用码)→ 该 case 的
// HTTP 401 / code 断言红; handler 把审计 reason 也抹成 generic → 审计 reason 断言红。
func TestLogin_AccountStateFailuresAreGeneric(t *testing.T) {
	now := time.Date(2026, 5, 31, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name          string
		status        userauth.UserStatus
		emailVerified bool
		password      string
		wantReason    string
	}{
		{"disabled", userauth.UserStatusDisabled, true, "secret", "user_disabled"},
		{"locked", userauth.UserStatusLocked, true, "secret", "user_locked"},
		{"reset_required", userauth.UserStatusResetRequired, true, "secret", "password_reset_required"},
		{"unverified", userauth.UserStatusActive, false, "secret", "email_unverified"},
		{"wrong_password", userauth.UserStatusActive, true, "wrong", "invalid_credentials"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := newGatewayMemoryAuthStore(now)
			seedLoginUser(t, base, "u@example.test", "secret", tc.status, tc.emailVerified)
			r, events := newLoginTestHandler(t, now, base, nil, true) // RequireVerified=true; 无限流隔离本测

			rec := serveJSON(t, r, http.MethodPost, "/v1/auth/login", map[string]any{
				"tenant_id": 1, "email": "u@example.test", "password": tc.password,
			})
			// 对外: 统一 generic 401 invalid_credentials, 不泄露账号状态。
			assertHTTPStatus(t, rec, http.StatusUnauthorized)
			if code := loginErrorCode(t, rec); code != "invalid_credentials" {
				t.Fatalf("%s: public error code=%q, want invalid_credentials (no status enumeration)", tc.name, code)
			}
			// 对内: 审计事件仍记真实 reason, 操作员可见。
			if reason := lastLoginFailedReason(t, events); reason != tc.wantReason {
				t.Fatalf("%s: audit reason_class=%q, want %q (audit must keep the real reason while user sees generic)", tc.name, reason, tc.wantReason)
			}
		})
	}
}

// TestLogin_ThrottleKeyedByIPNotTenant 钉住限流 key 的来源: 用可信 client IP, 不用未认证可伪造
// 的 body tenant_id。否则攻击者只要每次换一个 tenant_id 就能绕过 CPU 防护(用任意值刷满 argon2)。
//
// mutation: 把限流 key 改成含 body tenant_id(如 fmt.Sprintf("%d|%s", tenantID, ip))→ tenant=2
// 是新 key → 第二次不再 429(走到 Authenticate)→ 本测红。
func TestLogin_ThrottleKeyedByIPNotTenant(t *testing.T) {
	now := time.Date(2026, 5, 31, 9, 0, 0, 0, time.UTC)
	base := newGatewayMemoryAuthStore(now)
	seedLoginUser(t, base, "u@example.test", "secret", userauth.UserStatusActive, true)
	// WindowLimit=1: 同 IP 第一次失败后第二次即在限流闸被拒。
	limiter := loginthrottle.New(loginthrottle.Config{WindowLimit: 1, InFlightLimit: 10, BanAfter: 100, Now: func() time.Time { return now }})
	r, _ := newLoginTestHandler(t, now, base, limiter, false)

	// tenant=1 错口令 → 401, 记一次失败(按 IP)。
	rec1 := serveJSON(t, r, http.MethodPost, "/v1/auth/login", map[string]any{
		"tenant_id": 1, "email": "u@example.test", "password": "wrong",
	})
	assertHTTPStatus(t, rec1, http.StatusUnauthorized)

	// 同 IP 换 tenant=2 → 若按 IP 限流(正确)则仍 429; 若按 tenant 限流则会放行(被本测抓到)。
	rec2 := serveJSON(t, r, http.MethodPost, "/v1/auth/login", map[string]any{
		"tenant_id": 2, "email": "someone@example.test", "password": "whatever",
	})
	assertHTTPStatus(t, rec2, http.StatusTooManyRequests)
}
