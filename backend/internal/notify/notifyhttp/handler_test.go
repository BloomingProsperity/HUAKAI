package notifyhttp

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
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/notify"
)

func TestUserPutSettingsUsesSessionScopeAndMasksSecret(t *testing.T) {
	service := &recordingSettingsService{}
	router := chi.NewRouter()
	MountUserRoutes(router, UserDeps{Service: service})
	body := `{
		"notify_type":"webhook",
		"webhook_url":"https://hooks.example.test/low-balance",
		"webhook_secret":"plain-secret",
		"balance_threshold":"9.00000000"
	}`
	req := httptest.NewRequest(http.MethodPut, "/v1/users/me/notifications", strings.NewReader(body))
	req = req.WithContext(sessionauth.ContextWithSession(req.Context(), sessionauth.SessionIdentity{
		TenantID: 7,
		UserID:   42,
	}))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.saved.TenantID != 7 || service.saved.UserID != 42 {
		t.Fatalf("saved scope tenant=%d user=%d want 7/42; MUTATION: trusting request body scope would fail", service.saved.TenantID, service.saved.UserID)
	}
	if strings.Contains(rec.Body.String(), "plain-secret") {
		t.Fatalf("response leaked webhook_secret: %s", rec.Body.String())
	}
	var resp settingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response json: %v", err)
	}
	if !resp.WebhookSecretConfigured {
		t.Fatalf("webhook_secret_configured=false want true")
	}
}

func TestAdminTenantOperatorDefaultsToScopedTenant(t *testing.T) {
	service := &recordingSettingsService{}
	router := chi.NewRouter()
	MountAdminRoutes(router, AdminDeps{
		Auth: fakeAdminAuth{identity: admin.AdminIdentity{
			TokenID:       99,
			Role:          admin.RoleTenantOperator,
			ScopeTenantID: 7,
		}},
		Service: service,
	})
	body := `{"notify_type":"email","notification_email":"ops@example.test","balance_threshold":"3.00000000"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/admin/users/42/notifications", strings.NewReader(body))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.saved.TenantID != 7 || service.saved.UserID != 42 {
		t.Fatalf("saved scope tenant=%d user=%d want scoped tenant 7 and path user 42", service.saved.TenantID, service.saved.UserID)
	}
	if service.saved.UpdatedBy != "admin:99" {
		t.Fatalf("updated_by=%q want admin:99", service.saved.UpdatedBy)
	}
}

func TestAdminTenantOperatorCannotCrossTenant(t *testing.T) {
	service := &recordingSettingsService{}
	router := chi.NewRouter()
	MountAdminRoutes(router, AdminDeps{
		Auth: fakeAdminAuth{identity: admin.AdminIdentity{
			TokenID:       99,
			Role:          admin.RoleTenantOperator,
			ScopeTenantID: 7,
		}},
		Service: service,
	})
	req := httptest.NewRequest(http.MethodPut, "/v1/admin/users/42/notifications?tenant_id=8", bytes.NewBufferString(`{"notify_type":"none"}`))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.upserts != 0 {
		t.Fatalf("upserts=%d want 0; MUTATION: skipping tenant scope check should write", service.upserts)
	}
}

func TestUserPutGotifyDefaultsPriorityWhenOmitted(t *testing.T) {
	service := &recordingSettingsService{}
	router := chi.NewRouter()
	MountUserRoutes(router, UserDeps{Service: service})
	body := `{
		"notify_type":"gotify",
		"gotify_url":"https://gotify.example.test/message",
		"gotify_token":"plain-gotify-token",
		"balance_threshold":"9.00000000"
	}`
	req := httptest.NewRequest(http.MethodPut, "/v1/users/me/notifications", strings.NewReader(body))
	req = req.WithContext(sessionauth.ContextWithSession(req.Context(), sessionauth.SessionIdentity{
		TenantID: 7,
		UserID:   42,
	}))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.saved.GotifyPriority != 5 {
		t.Fatalf("saved gotify_priority=%d want default 5; MUTATION: omitting request defaulting should leave this 0", service.saved.GotifyPriority)
	}
	var resp settingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response json: %v", err)
	}
	if resp.GotifyPriority != 5 {
		t.Fatalf("response gotify_priority=%d want 5", resp.GotifyPriority)
	}
	if strings.Contains(rec.Body.String(), "plain-gotify-token") {
		t.Fatalf("response leaked gotify_token: %s", rec.Body.String())
	}
}

func TestUserPutGotifyRejectsOutOfRangePriority(t *testing.T) {
	service := &recordingSettingsService{}
	router := chi.NewRouter()
	MountUserRoutes(router, UserDeps{Service: service})
	body := `{
		"notify_type":"gotify",
		"gotify_url":"https://gotify.example.test/message",
		"gotify_token":"plain-gotify-token",
		"gotify_priority":99,
		"balance_threshold":"9.00000000"
	}`
	req := httptest.NewRequest(http.MethodPut, "/v1/users/me/notifications", strings.NewReader(body))
	req = req.WithContext(sessionauth.ContextWithSession(req.Context(), sessionauth.SessionIdentity{
		TenantID: 7,
		UserID:   42,
	}))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s; MUTATION: removing priority range validation should return 200", rec.Code, rec.Body.String())
	}
	if service.upserts != 0 {
		t.Fatalf("upserts=%d want 0", service.upserts)
	}
}

type recordingSettingsService struct {
	saved   notify.Settings
	upserts int
}

func (s *recordingSettingsService) GetSettings(context.Context, int64, int64) (notify.Settings, error) {
	return s.saved, nil
}

func (s *recordingSettingsService) UpsertSettings(_ context.Context, settings notify.Settings) (notify.Settings, error) {
	normalized, err := notify.ValidateSettings(settings)
	if err != nil {
		return notify.Settings{}, err
	}
	normalized.UpdatedAt = time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	s.saved = normalized
	s.upserts++
	return normalized, nil
}

type fakeAdminAuth struct {
	identity admin.AdminIdentity
	err      error
}

func (a fakeAdminAuth) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if a.err != nil {
		return admin.AdminIdentity{}, a.err
	}
	return a.identity, nil
}
