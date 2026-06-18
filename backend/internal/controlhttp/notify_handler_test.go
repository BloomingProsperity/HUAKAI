package controlhttp

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
	service := &notifyRecordingSettingsService{}
	router := chi.NewRouter()
	MountNotifyUserRoutes(router, NotifyUserDeps{Service: service})
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
	var resp notifySettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response json: %v", err)
	}
	if !resp.WebhookSecretConfigured {
		t.Fatalf("webhook_secret_configured=false want true")
	}
}

func TestAdminTenantOperatorDefaultsToScopedTenant(t *testing.T) {
	service := &notifyRecordingSettingsService{}
	router := chi.NewRouter()
	MountNotifyAdminRoutes(router, NotifyAdminDeps{
		Auth: notifyFakeAdminAuth{identity: admin.AdminIdentity{
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
	service := &notifyRecordingSettingsService{}
	router := chi.NewRouter()
	MountNotifyAdminRoutes(router, NotifyAdminDeps{
		Auth: notifyFakeAdminAuth{identity: admin.AdminIdentity{
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
	service := &notifyRecordingSettingsService{}
	router := chi.NewRouter()
	MountNotifyUserRoutes(router, NotifyUserDeps{Service: service})
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
	var resp notifySettingsResponse
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
	service := &notifyRecordingSettingsService{}
	router := chi.NewRouter()
	MountNotifyUserRoutes(router, NotifyUserDeps{Service: service})
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

// 守 extra_emails 双向接线: PUT 的 extra_emails 经 notifyRequestToSettings 映射进 settings(service 收到),
// 且 notifyResponseFromSettings 把它回写进响应(read-modify-write)。用两条判别邮箱。
// MUTATION: notifyRequestToSettings 漏映射 ExtraEmails → saved.ExtraEmails 空 → 红;
//           notifyResponseFromSettings 漏映射 → 响应缺 extra_emails → 红。
func TestUserPutRoundTripsExtraEmails(t *testing.T) {
	service := &notifyRecordingSettingsService{}
	router := chi.NewRouter()
	MountNotifyUserRoutes(router, NotifyUserDeps{Service: service})
	body := `{
		"notify_type":"email",
		"notification_email":"primary@example.test",
		"extra_emails":["cc1@example.test","cc2@example.test"],
		"balance_threshold":"9.00000000"
	}`
	req := httptest.NewRequest(http.MethodPut, "/v1/users/me/notifications", strings.NewReader(body))
	req = req.WithContext(sessionauth.ContextWithSession(req.Context(), sessionauth.SessionIdentity{TenantID: 7, UserID: 42}))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(service.saved.ExtraEmails) != 2 || service.saved.ExtraEmails[0] != "cc1@example.test" || service.saved.ExtraEmails[1] != "cc2@example.test" {
		t.Fatalf("saved ExtraEmails=%v want [cc1 cc2] (request→settings mapping)", service.saved.ExtraEmails)
	}
	var resp notifySettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response json: %v", err)
	}
	if len(resp.ExtraEmails) != 2 || resp.ExtraEmails[0] != "cc1@example.test" || resp.ExtraEmails[1] != "cc2@example.test" {
		t.Fatalf("response extra_emails=%v want [cc1 cc2] (settings→response mapping, read-modify-write)", resp.ExtraEmails)
	}
}

// 守映射真到达校验: 非法 extra_emails 必 400(经 ValidateSettings mail.ParseAddress), 不落库。
// 这同时证明 notifyRequestToSettings 确实映射了 ExtraEmails —— 若漏映射, 校验只见空列表会放行 → 200。
// MUTATION: notifyRequestToSettings 漏映射 ExtraEmails → 非法值不进校验 → 200 → 红。
func TestUserPutRejectsInvalidExtraEmail(t *testing.T) {
	service := &notifyRecordingSettingsService{}
	router := chi.NewRouter()
	MountNotifyUserRoutes(router, NotifyUserDeps{Service: service})
	body := `{"notify_type":"email","notification_email":"primary@example.test","extra_emails":["not-an-email"]}`
	req := httptest.NewRequest(http.MethodPut, "/v1/users/me/notifications", strings.NewReader(body))
	req = req.WithContext(sessionauth.ContextWithSession(req.Context(), sessionauth.SessionIdentity{TenantID: 7, UserID: 42}))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 (invalid extra_emails rejected via ValidateSettings); body=%s", rec.Code, rec.Body.String())
	}
	if service.upserts != 0 {
		t.Fatalf("upserts=%d want 0 (invalid input must not persist)", service.upserts)
	}
}

// 守数量上限: 超过 10 条 extra_emails → 400(ValidateSettings count 校验), 不落库。
// 证明映射的列表整体到达计数校验。MUTATION: 漏映射 → 校验见空列表放行 → 200 → 红。
func TestUserPutRejectsTooManyExtraEmails(t *testing.T) {
	service := &notifyRecordingSettingsService{}
	router := chi.NewRouter()
	MountNotifyUserRoutes(router, NotifyUserDeps{Service: service})
	addrs := make([]string, 0, 11)
	for i := 0; i < 11; i++ {
		addrs = append(addrs, `"cc`+string(rune('a'+i))+`@example.test"`)
	}
	body := `{"notify_type":"none","extra_emails":[` + strings.Join(addrs, ",") + `]}`
	req := httptest.NewRequest(http.MethodPut, "/v1/users/me/notifications", strings.NewReader(body))
	req = req.WithContext(sessionauth.ContextWithSession(req.Context(), sessionauth.SessionIdentity{TenantID: 7, UserID: 42}))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 (>10 extra_emails rejected); body=%s", rec.Code, rec.Body.String())
	}
	if service.upserts != 0 {
		t.Fatalf("upserts=%d want 0", service.upserts)
	}
}

type notifyRecordingSettingsService struct {
	saved   notify.Settings
	upserts int
}

func (s *notifyRecordingSettingsService) GetSettings(context.Context, int64, int64) (notify.Settings, error) {
	return s.saved, nil
}

func (s *notifyRecordingSettingsService) UpsertSettings(_ context.Context, settings notify.Settings) (notify.Settings, error) {
	normalized, err := notify.ValidateSettings(settings)
	if err != nil {
		return notify.Settings{}, err
	}
	normalized.UpdatedAt = time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	s.saved = normalized
	s.upserts++
	return normalized, nil
}

type notifyFakeAdminAuth struct {
	identity admin.AdminIdentity
	err      error
}

func (a notifyFakeAdminAuth) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if a.err != nil {
		return admin.AdminIdentity{}, a.err
	}
	return a.identity, nil
}
