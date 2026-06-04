package twofahttp

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

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/twofa"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

func TestSetupReturnsSecretOnceAndStatusDoesNotEchoSecretOrBackupCodes(t *testing.T) {
	now := time.Date(2026, 6, 3, 13, 0, 0, 0, time.UTC)
	service := twofa.NewService(twofa.NewMemoryStore(), mustHTTPKeyProvider(t), twofa.WithNow(func() time.Time { return now }))
	settings := platformsettings.NewService(platformsettings.NewMemoryStore(), nil)
	if _, err := settings.Upsert(context.Background(), platformsettings.UpsertInput{
		Key: platformsettings.KeyTwoFactorEnabled, Value: "true", UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("enable platform setting: %v", err)
	}
	router := twoFATestRouter(service, settings)

	setupRec := serveTwoFAJSON(t, router, http.MethodPost, "/v1/auth/2fa/setup", map[string]any{
		"account_name": "alice@example.test",
	})
	assertTwoFAStatus(t, setupRec, http.StatusCreated)
	var setup struct {
		Secret      string   `json:"secret"`
		QRData      string   `json:"qr_data"`
		BackupCodes []string `json:"backup_codes"`
	}
	if err := json.Unmarshal(setupRec.Body.Bytes(), &setup); err != nil {
		t.Fatalf("decode setup: %v body=%s", err, setupRec.Body.String())
	}
	if setup.Secret == "" || len(setup.BackupCodes) != twofa.DefaultBackupCodeCount || !strings.Contains(setup.QRData, setup.Secret) {
		t.Fatalf("setup response missing one-time material: %+v", setup)
	}

	statusRec := serveTwoFAJSON(t, router, http.MethodGet, "/v1/auth/2fa/status", nil)
	assertTwoFAStatus(t, statusRec, http.StatusOK)
	body := statusRec.Body.String()
	for _, sentinel := range append([]string{setup.Secret}, setup.BackupCodes...) {
		if strings.Contains(body, sentinel) {
			t.Fatalf("status leaked one-time 2FA material %q in %s", sentinel, body)
		}
	}
}

func TestRegenerateBackupCodesRequiresFreshCodeProof(t *testing.T) {
	now := time.Date(2026, 6, 3, 13, 10, 0, 0, time.UTC)
	service := twofa.NewService(twofa.NewMemoryStore(), mustHTTPKeyProvider(t), twofa.WithNow(func() time.Time { return now }))
	settings := platformsettings.NewService(platformsettings.NewMemoryStore(), nil)
	if _, err := settings.Upsert(context.Background(), platformsettings.UpsertInput{
		Key: platformsettings.KeyTwoFactorEnabled, Value: "true", UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("enable platform setting: %v", err)
	}
	setup, err := service.Setup(context.Background(), twofa.SetupInput{TenantID: 1, UserID: 1001, AccountName: "alice@example.test"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if _, err := service.Enable(context.Background(), twofa.VerifyInput{
		TenantID: 1, UserID: 1001, Code: httpCodeFromSecret(t, setup.Secret, now),
	}); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	router := twoFATestRouter(service, settings)

	rec := serveTwoFAJSON(t, router, http.MethodPost, "/v1/auth/2fa/backup-codes/regenerate", map[string]any{
		"code": "000000",
	})
	assertTwoFAStatus(t, rec, http.StatusUnauthorized)
	if code := twoFAErrorCode(t, rec); code != "two_factor_invalid" {
		t.Fatalf("error code=%q want two_factor_invalid", code)
	}

	rec = serveTwoFAJSON(t, router, http.MethodPost, "/v1/auth/2fa/backup-codes/regenerate", map[string]any{
		"code": httpCodeFromSecret(t, setup.Secret, now),
	})
	assertTwoFAStatus(t, rec, http.StatusOK)
	var resp struct {
		BackupCodes []string `json:"backup_codes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode regenerate response: %v body=%s", err, rec.Body.String())
	}
	if len(resp.BackupCodes) != twofa.DefaultBackupCodeCount {
		t.Fatalf("backup codes len=%d want %d", len(resp.BackupCodes), twofa.DefaultBackupCodeCount)
	}
}

// TestDisableRequiresFreshCodeProofAndKeeps2FAEnabled guards the stolen-session
// regression: a bearer session alone must not be enough to turn off 2FA. Mutation
// check: revert /disable to call Disable without VerifyLogin and the first case
// returns 200 and flips enabled=false, making both assertions red.
func TestDisableRequiresFreshCodeProofAndKeeps2FAEnabled(t *testing.T) {
	now := time.Date(2026, 6, 4, 9, 0, 0, 0, time.UTC)
	service := twofa.NewService(twofa.NewMemoryStore(), mustHTTPKeyProvider(t), twofa.WithNow(func() time.Time { return now }))
	settings := enabledTwoFAHTTPSettings(t)
	setup, err := service.Setup(context.Background(), twofa.SetupInput{TenantID: 1, UserID: 1001, AccountName: "alice@example.test"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if _, err := service.Enable(context.Background(), twofa.VerifyInput{
		TenantID: 1, UserID: 1001, Code: httpCodeFromSecret(t, setup.Secret, now),
	}); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	router := twoFATestRouter(service, settings)

	for _, body := range []map[string]any{
		{},
		{"code": "000000"},
	} {
		rec := serveTwoFAJSON(t, router, http.MethodPost, "/v1/auth/2fa/disable", body)
		assertTwoFAStatus(t, rec, http.StatusUnauthorized)
		if code := twoFAErrorCode(t, rec); code != "two_factor_invalid" {
			t.Fatalf("error code=%q want two_factor_invalid", code)
		}
		status, err := service.Status(context.Background(), 1, 1001)
		if err != nil {
			t.Fatalf("Status after rejected disable: %v", err)
		}
		if !status.Enabled {
			t.Fatalf("2FA must remain enabled after rejected disable; body=%v", body)
		}
	}

	rec := serveTwoFAJSON(t, router, http.MethodPost, "/v1/auth/2fa/disable", map[string]any{
		"code": setup.BackupCodes[0],
	})
	assertTwoFAStatus(t, rec, http.StatusOK)
	status, err := service.Status(context.Background(), 1, 1001)
	if err != nil {
		t.Fatalf("Status after valid disable: %v", err)
	}
	if status.Enabled {
		t.Fatal("valid backup-code proof should disable 2FA")
	}
}

// TestTwoFAStateChangesRevokeOtherSessions proves successful 2FA state changes
// invalidate other sessions while keeping the current authenticated family. Mutation
// check: call the user-wide revoker or omit CurrentFamilyID and this test goes red
// without weakening the state-change assertions.
func TestTwoFAStateChangesRevokeOtherSessions(t *testing.T) {
	now := time.Date(2026, 6, 4, 9, 30, 0, 0, time.UTC)
	service := twofa.NewService(twofa.NewMemoryStore(), mustHTTPKeyProvider(t), twofa.WithNow(func() time.Time { return now }))
	settings := enabledTwoFAHTTPSettings(t)
	setup, err := service.Setup(context.Background(), twofa.SetupInput{TenantID: 1, UserID: 1001, AccountName: "alice@example.test"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	revoker := &recordingSessionRevoker{}
	router := twoFATestRouterWithDepsAndIdent(Deps{Service: service, Settings: settings, Sessions: revoker}, sessionauth.SessionIdentity{
		TenantID: 1, UserID: 1001, FamilyID: "current-family",
	})

	enableRec := serveTwoFAJSON(t, router, http.MethodPost, "/v1/auth/2fa/enable", map[string]any{
		"code": httpCodeFromSecret(t, setup.Secret, now),
	})
	assertTwoFAStatus(t, enableRec, http.StatusOK)
	disableRec := serveTwoFAJSON(t, router, http.MethodPost, "/v1/auth/2fa/disable", map[string]any{
		"code": httpCodeFromSecret(t, setup.Secret, now),
	})
	assertTwoFAStatus(t, disableRec, http.StatusOK)

	if got := len(revoker.calls); got != 2 {
		t.Fatalf("session revoke calls=%d want 2; calls=%+v", got, revoker.calls)
	}
	for i, call := range revoker.calls {
		if call.TenantID != 1 || call.UserID != 1001 || call.CurrentFamilyID != "current-family" ||
			call.Reason != "two_factor_state_changed" {
			t.Fatalf("revoke-others call %d=%+v, want tenant/user/current-family scoped two_factor_state_changed", i, call)
		}
	}
}

// TestDisableKeepsCurrentSessionAndRevokesOtherSessions is the user-visible
// regression test for the over-fix: after a valid disable proof, the browser that
// made the request must stay logged in while a second session is invalidated.
// Mutation check: use RevokeUser in the handler path and the current Validate call
// returns ErrFamilyRevoked; remove revocation and the other-session assertion fails.
func TestDisableKeepsCurrentSessionAndRevokesOtherSessions(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 4, 11, 30, 0, 0, time.UTC)
	service := twofa.NewService(twofa.NewMemoryStore(), mustHTTPKeyProvider(t), twofa.WithNow(func() time.Time { return now }))
	settings := enabledTwoFAHTTPSettings(t)
	setup, err := service.Setup(ctx, twofa.SetupInput{TenantID: 1, UserID: 1001, AccountName: "alice@example.test"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if _, err := service.Enable(ctx, twofa.VerifyInput{
		TenantID: 1, UserID: 1001, Code: httpCodeFromSecret(t, setup.Secret, now),
	}); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	sessionSvc := usersession.NewService(usersession.NewMemoryStore())
	sessionSvc.Now = func() time.Time { return now }
	sessionSvc.SigningKey = []byte("0123456789abcdef0123456789abcdef")
	sessionSvc.SessionTTL = time.Hour
	sessionSvc.RefreshTTL = time.Hour
	current, err := sessionSvc.Create(ctx, usersession.CreateInput{TenantID: 1, UserID: 1001, IP: "192.0.2.10", UserAgent: "Chrome/1"})
	if err != nil {
		t.Fatalf("Create current session: %v", err)
	}
	other, err := sessionSvc.Create(ctx, usersession.CreateInput{TenantID: 1, UserID: 1001, IP: "192.0.2.11", UserAgent: "Firefox/1"})
	if err != nil {
		t.Fatalf("Create other session: %v", err)
	}
	router := chi.NewRouter()
	router.Route("/v1/auth/2fa", func(r chi.Router) {
		r.Use(sessionauth.SessionMiddleware(sessionSvc, nil))
		MountRoutes(r, Deps{Service: service, Settings: settings, Sessions: sessionSvc})
	})

	rec := serveTwoFAJSONWithBearer(t, router, http.MethodPost, "/v1/auth/2fa/disable", map[string]any{
		"code": httpCodeFromSecret(t, setup.Secret, now),
	}, current.SessionToken, "192.0.2.10:443", "Chrome/1")

	assertTwoFAStatus(t, rec, http.StatusOK)
	if _, err := sessionSvc.Validate(ctx, current.SessionToken, "192.0.2.10", "Chrome/1"); err != nil {
		t.Fatalf("current session must remain valid after disable: %v", err)
	}
	if _, err := sessionSvc.Validate(ctx, other.SessionToken, "192.0.2.11", "Firefox/1"); !errors.Is(err, usersession.ErrFamilyRevoked) {
		t.Fatalf("other session validate=%v want ErrFamilyRevoked", err)
	}
}

func twoFATestRouter(service *twofa.Service, settings *platformsettings.Service) http.Handler {
	return twoFATestRouterWithDeps(Deps{Service: service, Settings: settings})
}

func twoFATestRouterWithDeps(d Deps) http.Handler {
	return twoFATestRouterWithDepsAndIdent(d, sessionauth.SessionIdentity{
		TenantID: 1, UserID: 1001, FamilyID: "test-family",
	})
}

func twoFATestRouterWithDepsAndIdent(d Deps, ident sessionauth.SessionIdentity) http.Handler {
	r := chi.NewRouter()
	r.Route("/v1/auth/2fa", func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				next.ServeHTTP(w, req.WithContext(sessionauth.ContextWithSession(req.Context(), ident)))
			})
		})
		MountRoutes(r, d)
	})
	return r
}

func enabledTwoFAHTTPSettings(t *testing.T) *platformsettings.Service {
	t.Helper()
	settings := platformsettings.NewService(platformsettings.NewMemoryStore(), nil)
	if _, err := settings.Upsert(context.Background(), platformsettings.UpsertInput{
		Key: platformsettings.KeyTwoFactorEnabled, Value: "true", UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("enable platform setting: %v", err)
	}
	return settings
}

type recordingSessionRevoker struct {
	calls []usersession.RevokeOthersInput
	err   error
}

func (r *recordingSessionRevoker) RevokeOthers(_ context.Context, in usersession.RevokeOthersInput) (int64, error) {
	r.calls = append(r.calls, in)
	if r.err != nil {
		return 0, r.err
	}
	return 1, nil
}

func TestTwoFAStateChangeReportsSessionRevokeFailure(t *testing.T) {
	now := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	service := twofa.NewService(twofa.NewMemoryStore(), mustHTTPKeyProvider(t), twofa.WithNow(func() time.Time { return now }))
	settings := enabledTwoFAHTTPSettings(t)
	setup, err := service.Setup(context.Background(), twofa.SetupInput{TenantID: 1, UserID: 1001, AccountName: "alice@example.test"})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	revoker := &recordingSessionRevoker{err: errors.New("session store down")}
	router := twoFATestRouterWithDeps(Deps{Service: service, Settings: settings, Sessions: revoker})

	rec := serveTwoFAJSON(t, router, http.MethodPost, "/v1/auth/2fa/enable", map[string]any{
		"code": httpCodeFromSecret(t, setup.Secret, now),
	})
	assertTwoFAStatus(t, rec, http.StatusServiceUnavailable)
	if code := twoFAErrorCode(t, rec); code != "session_revoke_failed" {
		t.Fatalf("error code=%q want session_revoke_failed", code)
	}
}

func mustHTTPKeyProvider(t *testing.T) credentialstore.KeyProvider {
	t.Helper()
	provider, err := credentialstore.NewStaticKeyProvider("twofa-http-test-key", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("NewStaticKeyProvider: %v", err)
	}
	return provider
}

func serveTwoFAJSON(t *testing.T, h http.Handler, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return serveTwoFAJSONWithBearer(t, h, method, target, body, "", "", "")
}

func serveTwoFAJSONWithBearer(t *testing.T, h http.Handler, method, target string, body any, token string, remoteAddr string, userAgent string) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func assertTwoFAStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want %d body=%s", rec.Code, want, rec.Body.String())
	}
}

func twoFAErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var resp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error: %v body=%s", err, rec.Body.String())
	}
	return resp.Error.Code
}

func httpCodeFromSecret(t *testing.T, encoded string, now time.Time) string {
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
