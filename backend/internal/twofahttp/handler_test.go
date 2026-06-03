package twofahttp

import (
	"bytes"
	"context"
	"encoding/json"
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

func twoFATestRouter(service *twofa.Service, settings *platformsettings.Service) http.Handler {
	r := chi.NewRouter()
	r.Route("/v1/auth/2fa", func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				ident := sessionauth.SessionIdentity{TenantID: 1, UserID: 1001}
				next.ServeHTTP(w, req.WithContext(sessionauth.ContextWithSession(req.Context(), ident)))
			})
		})
		MountRoutes(r, Deps{Service: service, Settings: settings})
	})
	return r
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
