package gatewayhttp

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

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	mailinfra "github.com/BloomingProsperity/HUAKAI/internal/email"
)

func TestAdminEmailSettingsPutEncryptsPasswordAndGetMasks(t *testing.T) {
	keys := testGatewayEmailKeys(t)
	store := newGatewayEmailSettingsStore()
	handler := newAdminEmailSettingsTestRouter(AdminEmailSettingsDeps{
		Auth:  adminEmailAuthStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RolePlatformAdmin}},
		Store: store,
		Keys:  keys,
	})
	rec := serveAdminEmailJSON(t, handler, http.MethodPut, "/v1/admin/email/settings", map[string]any{
		"tenant_id": 1, "smtp_host": "smtp.example.test", "smtp_port": 587,
		"smtp_username": "smtp-user", "smtp_password": "smtp-secret",
		"smtp_from": "noreply@example.test", "smtp_from_name": "HUAKAI",
		"smtp_use_tls": true, "email_verify_enabled": true,
	})
	assertHTTPStatus(t, rec, http.StatusOK)
	stored := store.settings[1][mailinfra.SettingMailPassword]
	if stored == "" || stored == "smtp-secret" {
		t.Fatalf("password was not encrypted at rest: %q", stored)
	}
	plain, err := mailinfra.DecodeSecret(context.Background(), keys, 1, stored)
	if err != nil || plain != "smtp-secret" {
		t.Fatalf("DecodeSecret plain=%q err=%v", plain, err)
	}

	rec = serveAdminEmailJSON(t, handler, http.MethodGet, "/v1/admin/email/settings?tenant_id=1", nil)
	assertHTTPStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "smtp-secret") || strings.Contains(rec.Body.String(), stored) {
		t.Fatalf("GET leaked password material: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"configured":true`) {
		t.Fatalf("GET did not expose password configured marker: %s", rec.Body.String())
	}
}

func TestAdminEmailSettingsRejectsCrossTenantOperator(t *testing.T) {
	keys := testGatewayEmailKeys(t)
	handler := newAdminEmailSettingsTestRouter(AdminEmailSettingsDeps{
		Auth:  adminEmailAuthStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		Store: newGatewayEmailSettingsStore(),
		Keys:  keys,
	})
	rec := serveAdminEmailJSON(t, handler, http.MethodPut, "/v1/admin/email/settings", map[string]any{
		"tenant_id": 8, "smtp_host": "smtp.example.test",
	})
	assertHTTPStatus(t, rec, http.StatusForbidden)
}

func TestAdminEmailTestUsesTenantScopedSettings(t *testing.T) {
	keys := testGatewayEmailKeys(t)
	store := newGatewayEmailSettingsStore()
	store.settings[7] = completeGatewayEmailSettings(t, keys, 7)
	var gotSettings mailinfra.SMTPSettings
	var gotMessage mailinfra.Message
	handler := newAdminEmailSettingsTestRouter(AdminEmailSettingsDeps{
		Auth:  adminEmailAuthStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		Store: store,
		Keys:  keys,
		TestDispatch: func(_ context.Context, settings mailinfra.SMTPSettings, msg mailinfra.Message) error {
			gotSettings = settings
			gotMessage = msg
			return nil
		},
	})
	rec := serveAdminEmailJSON(t, handler, http.MethodPost, "/v1/admin/email/test", map[string]any{
		"tenant_id": 7, "to": "owner@example.test",
	})
	assertHTTPStatus(t, rec, http.StatusOK)
	if gotSettings.TenantID != 7 || gotSettings.Password != "smtp-secret" || gotMessage.To != "owner@example.test" {
		t.Fatalf("dispatch mismatch: settings=%+v message=%+v", gotSettings, gotMessage)
	}
}

type adminEmailAuthStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s adminEmailAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return s.ident, s.err
}

type gatewayEmailSettingsStore struct {
	settings map[int64]mailinfra.StoredSettings
}

func newGatewayEmailSettingsStore() *gatewayEmailSettingsStore {
	return &gatewayEmailSettingsStore{settings: make(map[int64]mailinfra.StoredSettings)}
}

func (s *gatewayEmailSettingsStore) Load(_ context.Context, tenantID int64) (mailinfra.StoredSettings, error) {
	return s.settings[tenantID], nil
}

func (s *gatewayEmailSettingsStore) List(_ context.Context, tenantID int64) ([]mailinfra.StoredSetting, error) {
	raw := s.settings[tenantID]
	out := make([]mailinfra.StoredSetting, 0, len(raw))
	for key, value := range raw {
		out = append(out, mailinfra.StoredSetting{
			Key: key, Value: value, UpdatedAt: time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC), UpdatedBy: "11",
		})
	}
	return out, nil
}

func (s *gatewayEmailSettingsStore) Save(_ context.Context, tenantID int64, values map[string]string, _ string) error {
	if s.settings[tenantID] == nil {
		s.settings[tenantID] = make(mailinfra.StoredSettings)
	}
	for key, value := range values {
		s.settings[tenantID][key] = value
	}
	return nil
}

func (s *gatewayEmailSettingsStore) ListActiveTenantIDs(context.Context) ([]int64, error) {
	ids := make([]int64, 0, len(s.settings))
	for id := range s.settings {
		ids = append(ids, id)
	}
	return ids, nil
}

func newAdminEmailSettingsTestRouter(deps AdminEmailSettingsDeps) http.Handler {
	r := chi.NewRouter()
	r.Route("/v1/admin/email", func(r chi.Router) {
		MountAdminEmailSettingsRoutes(r, deps)
	})
	return r
}

func serveAdminEmailJSON(t *testing.T, h http.Handler, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func testGatewayEmailKeys(t *testing.T) credentialstore.KeyProvider {
	t.Helper()
	keys, err := credentialstore.NewStaticKeyProvider("gateway-email-test", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("NewStaticKeyProvider: %v", err)
	}
	return keys
}

func completeGatewayEmailSettings(t *testing.T, keys credentialstore.KeyProvider, tenantID int64) mailinfra.StoredSettings {
	t.Helper()
	secret, err := mailinfra.EncodeSecret(context.Background(), keys, tenantID, "smtp-secret")
	if err != nil {
		t.Fatalf("EncodeSecret: %v", err)
	}
	return mailinfra.StoredSettings{
		mailinfra.SettingMailHost:          "smtp.example.test",
		mailinfra.SettingMailPort:          "587",
		mailinfra.SettingMailUsername:      "smtp-user",
		mailinfra.SettingMailPassword:      secret,
		mailinfra.SettingMailFrom:          "noreply@example.test",
		mailinfra.SettingVerifyRequirement: "true",
	}
}
