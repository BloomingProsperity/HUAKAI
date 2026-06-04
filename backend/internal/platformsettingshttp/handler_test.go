package platformsettingshttp

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

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

func TestHandlerGETListTenantOperatorGets403(t *testing.T) {
	svc := &serviceStub{}
	handler := newTestRouter(Deps{
		Auth:    authStub{ident: admin.AdminIdentity{TokenID: 22, Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		Service: svc,
	})

	rec := serveJSON(t, handler, http.MethodGet, "/v1/admin/platform-settings/", nil)

	assertStatus(t, rec, http.StatusForbidden)
	assertErrorCode(t, rec, "admin_forbidden")
	if svc.listCalls != 0 {
		t.Fatalf("tenant operator reached service List %d times", svc.listCalls)
	}
}

func TestHandlerPUTTenantOperatorGets403(t *testing.T) {
	svc := &serviceStub{}
	handler := newTestRouter(Deps{
		Auth:    authStub{ident: admin.AdminIdentity{TokenID: 22, Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		Service: svc,
	})

	rec := serveJSON(t, handler, http.MethodPut, "/v1/admin/platform-settings/promo_enabled", map[string]any{
		"value": "true",
	})

	assertStatus(t, rec, http.StatusForbidden)
	assertErrorCode(t, rec, "admin_forbidden")
	if svc.upsertCalls != 0 {
		t.Fatalf("tenant operator reached service Upsert %d times", svc.upsertCalls)
	}
}

func TestHandlerGETSingleAbsentKeyReturnsDefault(t *testing.T) {
	svc := &serviceStub{
		getResult: platformsettings.StoredSetting{
			Key:    platformsettings.KeyRegistrationEnabled,
			Value:  "false",
			Source: platformsettings.SourceDefault,
		},
	}
	handler := newTestRouter(Deps{
		Auth:    authStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RolePlatformAdmin}},
		Service: svc,
	})

	rec := serveJSON(t, handler, http.MethodGet, "/v1/admin/platform-settings/registration_enabled", nil)

	assertStatus(t, rec, http.StatusOK)
	got := decodeSettingResponse(t, rec)
	if got.Key != "registration_enabled" || got.Value != "false" || got.Source != "default" {
		t.Fatalf("response=%+v want registration_enabled false/default", got)
	}
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw response: %v", err)
	}
	if _, ok := raw["updated_at"]; !ok {
		t.Fatalf("updated_at missing from default response: %s", rec.Body.String())
	}
	if svc.getCalls != 1 || svc.lastGetKey != platformsettings.KeyRegistrationEnabled {
		t.Fatalf("service get calls=%d key=%q", svc.getCalls, svc.lastGetKey)
	}
}

func TestHandlerPUTUnknownKeyGets400BeforeService(t *testing.T) {
	svc := &serviceStub{}
	handler := newTestRouter(Deps{
		Auth:    authStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RolePlatformAdmin}},
		Service: svc,
	})

	rec := serveJSON(t, handler, http.MethodPut, "/v1/admin/platform-settings/smtp_password", map[string]any{
		"value": "do-not-store",
	})

	assertStatus(t, rec, http.StatusBadRequest)
	assertErrorCode(t, rec, "platform_setting_unknown_key")
	if svc.upsertCalls != 0 {
		t.Fatalf("unknown key reached service Upsert %d times", svc.upsertCalls)
	}
}

func TestHandlerPUTMissingValueFieldGets400(t *testing.T) {
	svc := &serviceStub{}
	handler := newTestRouter(Deps{
		Auth:    authStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RolePlatformAdmin}},
		Service: svc,
	})

	rec := serveJSON(t, handler, http.MethodPut, "/v1/admin/platform-settings/promo_enabled", map[string]any{
		"reason": "missing value",
	})

	assertStatus(t, rec, http.StatusBadRequest)
	assertErrorCode(t, rec, "platform_setting_value_required")
	if svc.upsertCalls != 0 {
		t.Fatalf("missing value reached service Upsert %d times", svc.upsertCalls)
	}
}

func TestHandlerPUTReasonOptionalWritesSetting(t *testing.T) {
	updatedAt := time.Date(2026, 6, 3, 5, 6, 7, 0, time.UTC)
	svc := &serviceStub{
		upsertResult: platformsettings.StoredSetting{
			Key:       platformsettings.KeyPromoEnabled,
			Value:     "true",
			Source:    platformsettings.SourceDB,
			UpdatedAt: updatedAt,
			UpdatedBy: "11",
		},
	}
	handler := newTestRouter(Deps{
		Auth:    authStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RolePlatformAdmin}},
		Service: svc,
	})

	rec := serveJSON(t, handler, http.MethodPut, "/v1/admin/platform-settings/promo_enabled", map[string]any{
		"value": "true",
	})

	assertStatus(t, rec, http.StatusOK)
	if svc.upsertCalls != 1 {
		t.Fatalf("upsert calls=%d want 1", svc.upsertCalls)
	}
	if svc.lastUpsert.Key != platformsettings.KeyPromoEnabled || svc.lastUpsert.Value != "true" ||
		svc.lastUpsert.UpdatedBy != "11" || svc.lastUpsert.ActorID != "11" ||
		svc.lastUpsert.ActorRole != admin.RolePlatformAdmin || svc.lastUpsert.Reason != "" {
		t.Fatalf("upsert input=%+v", svc.lastUpsert)
	}
	got := decodeSettingResponse(t, rec)
	if got.Value != "true" || got.Source != "db" || got.UpdatedAt == nil || *got.UpdatedBy != "11" {
		t.Fatalf("response=%+v want db true with metadata", got)
	}
}

// TestHandlerPUTCaptchaEnabledRequiresConfiguredSecret guards the new-api-like
// config-time gate: operators may boot with a missing secret, but cannot turn on
// CAPTCHA until this gateway process has the runtime secret configured. Mutation
// check: delete the guard and the enable request reaches Upsert while the disable
// request still proves the check is not a blanket write blocker.
func TestHandlerPUTCaptchaEnabledRequiresConfiguredSecret(t *testing.T) {
	svc := &serviceStub{
		upsertResult: platformsettings.StoredSetting{
			Key:    platformsettings.KeyCaptchaEnabled,
			Value:  "false",
			Source: platformsettings.SourceDB,
		},
	}
	handler := newTestRouter(Deps{
		Auth:                    authStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RolePlatformAdmin}},
		Service:                 svc,
		CaptchaSecretConfigured: false,
	})

	rec := serveJSON(t, handler, http.MethodPut, "/v1/admin/platform-settings/captcha_enabled", map[string]any{
		"value": "true",
	})

	assertStatus(t, rec, http.StatusBadRequest)
	assertErrorCode(t, rec, "captcha_secret_required")
	if svc.upsertCalls != 0 {
		t.Fatalf("missing secret enable reached Upsert %d times", svc.upsertCalls)
	}

	rec = serveJSON(t, handler, http.MethodPut, "/v1/admin/platform-settings/captcha_enabled", map[string]any{
		"value": "false",
	})

	assertStatus(t, rec, http.StatusOK)
	if svc.upsertCalls != 1 || svc.lastUpsert.Key != platformsettings.KeyCaptchaEnabled || svc.lastUpsert.Value != "false" {
		t.Fatalf("disable upsert calls=%d input=%+v", svc.upsertCalls, svc.lastUpsert)
	}
}

// TestHandlerGETCaptchaEnabledShowsMissingSecretHealth gives admins the runtime
// misconfiguration signal that replaced fail-boot. Mutation check: remove health
// decoration and the response still returns the setting but lacks the degraded
// missing-secret marker.
func TestHandlerGETCaptchaEnabledShowsMissingSecretHealth(t *testing.T) {
	svc := &serviceStub{
		getResult: platformsettings.StoredSetting{
			Key:    platformsettings.KeyCaptchaEnabled,
			Value:  "true",
			Source: platformsettings.SourceDB,
		},
	}
	handler := newTestRouter(Deps{
		Auth:                    authStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RolePlatformAdmin}},
		Service:                 svc,
		CaptchaSecretConfigured: false,
	})

	rec := serveJSON(t, handler, http.MethodGet, "/v1/admin/platform-settings/captcha_enabled", nil)

	assertStatus(t, rec, http.StatusOK)
	got := decodeSettingResponse(t, rec)
	if got.Health == nil || got.Health.Status != "degraded" ||
		got.Health.Issue != "turnstile_secret_missing" || got.Health.CaptchaSecretConfigured {
		t.Fatalf("captcha health=%+v want degraded missing-secret marker", got.Health)
	}
}

func TestHandlerPUTLargeBodyRejectedWith413(t *testing.T) {
	svc := &serviceStub{}
	handler := newTestRouter(Deps{
		Auth:    authStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RolePlatformAdmin}},
		Service: svc,
	})
	body := bytes.NewBufferString(`{"value":"` + strings.Repeat("x", 70<<10) + `"}`)
	req := httptest.NewRequest(http.MethodPut, "/v1/admin/platform-settings/promo_enabled", body)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusRequestEntityTooLarge)
	assertErrorCode(t, rec, "body_too_large")
	if svc.upsertCalls != 0 {
		t.Fatalf("oversize body reached service Upsert %d times", svc.upsertCalls)
	}
}

func TestHandlerNilServiceReturns503(t *testing.T) {
	handler := newTestRouter(Deps{
		Auth: authStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RolePlatformAdmin}},
	})

	rec := serveJSON(t, handler, http.MethodGet, "/v1/admin/platform-settings/", nil)

	assertStatus(t, rec, http.StatusServiceUnavailable)
	assertErrorCode(t, rec, "gateway_not_configured")
}

func TestHandlerGETListReturnsAllDefinedKeys(t *testing.T) {
	items := make([]platformsettings.StoredSetting, 0, len(platformsettings.AllKeys()))
	for _, key := range platformsettings.AllKeys() {
		value, _ := platformsettings.DefaultValue(key)
		source := platformsettings.SourceDefault
		if key == platformsettings.KeyPromoEnabled {
			value = "true"
			source = platformsettings.SourceDB
		}
		items = append(items, platformsettings.StoredSetting{Key: key, Value: value, Source: source})
	}
	handler := newTestRouter(Deps{
		Auth: authStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RolePlatformAdmin}},
		Service: &serviceStub{
			listResult: items,
		},
	})

	rec := serveJSON(t, handler, http.MethodGet, "/v1/admin/platform-settings/", nil)

	assertStatus(t, rec, http.StatusOK)
	got := decodeListResponse(t, rec)
	if len(got.Items) != len(platformsettings.AllKeys()) {
		t.Fatalf("items=%d want %d: %+v", len(got.Items), len(platformsettings.AllKeys()), got.Items)
	}
	seen := map[string]settingResponse{}
	for _, item := range got.Items {
		seen[item.Key] = item
	}
	if seen["promo_enabled"].Value != "true" || seen["promo_enabled"].Source != "db" {
		t.Fatalf("promo item=%+v want db true", seen["promo_enabled"])
	}
	if seen["registration_enabled"].Value != "false" || seen["registration_enabled"].Source != "default" {
		t.Fatalf("registration item=%+v want default false", seen["registration_enabled"])
	}
}

type authStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s authStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if s.err != nil {
		return admin.AdminIdentity{}, s.err
	}
	return s.ident, nil
}

type serviceStub struct {
	getCalls    int
	listCalls   int
	upsertCalls int

	lastGetKey   platformsettings.SettingKey
	lastUpsert   platformsettings.UpsertInput
	getResult    platformsettings.StoredSetting
	listResult   []platformsettings.StoredSetting
	upsertResult platformsettings.StoredSetting
	err          error
}

func (s *serviceStub) Get(_ context.Context, key platformsettings.SettingKey) (platformsettings.StoredSetting, error) {
	s.getCalls++
	s.lastGetKey = key
	if s.err != nil {
		return platformsettings.StoredSetting{}, s.err
	}
	return s.getResult, nil
}

func (s *serviceStub) List(context.Context) ([]platformsettings.StoredSetting, error) {
	s.listCalls++
	if s.err != nil {
		return nil, s.err
	}
	return append([]platformsettings.StoredSetting(nil), s.listResult...), nil
}

func (s *serviceStub) Upsert(_ context.Context, in platformsettings.UpsertInput) (platformsettings.StoredSetting, error) {
	s.upsertCalls++
	s.lastUpsert = in
	if s.err != nil {
		return platformsettings.StoredSetting{}, s.err
	}
	return s.upsertResult, nil
}

func newTestRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Route("/v1/admin/platform-settings", func(r chi.Router) {
		MountPlatformSettingsRoutes(r, d)
	})
	return r
}

func serveJSON(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want %d body=%s", rec.Code, want, rec.Body.String())
	}
}

func assertErrorCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v body=%s", err, rec.Body.String())
	}
	if body.Error.Code != want {
		t.Fatalf("error code=%q want %q body=%s", body.Error.Code, want, rec.Body.String())
	}
}

func decodeSettingResponse(t *testing.T, rec *httptest.ResponseRecorder) settingResponse {
	t.Helper()
	var body settingResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode setting response: %v body=%s", err, rec.Body.String())
	}
	return body
}

func decodeListResponse(t *testing.T, rec *httptest.ResponseRecorder) listResponse {
	t.Helper()
	var body listResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list response: %v body=%s", err, rec.Body.String())
	}
	return body
}

var _ Auth = authStub{}
var _ Service = (*serviceStub)(nil)
var _ = errors.Is
