package sitepublichttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

// expectedFieldCount is the exact size of the public projection: tenant_id +
// 8 booleans + 13 strings. It is asserted directly so that adding a key to the
// handler without updating this test (or vice versa) is caught, and so the
// CMB-5 leak test can prove "no extra key slipped in".
const expectedFieldCount = 1 + 8 + 13

// stubSettings serves canned setting values; absent keys fall back to the
// compiled default exactly like the real Service does on a DB-miss.
type stubSettings struct {
	values map[platformsettings.SettingKey]string
}

func (s stubSettings) Get(_ context.Context, key platformsettings.SettingKey) (platformsettings.StoredSetting, error) {
	if v, ok := s.values[key]; ok {
		return platformsettings.StoredSetting{Key: key, Value: v}, nil
	}
	def, _ := platformsettings.DefaultValue(key)
	return platformsettings.StoredSetting{Key: key, Value: def}, nil
}

func serveSiteConfig(t *testing.T, d Deps) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/site/config", nil)
	rec := httptest.NewRecorder()
	NewHandler(d).ServeHTTP(rec, req)
	return rec
}

func decodeSiteConfig(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return body
}

// T1: with an empty stub every field must equal the compiled default. Guards
// against a missing projection key or a wrong default. Mutation: flip the
// KeyTwoFactorEnabled default in types.go to "false" and this goes red.
func TestSiteConfigProjectsCompiledDefaults(t *testing.T) {
	rec := serveSiteConfig(t, Deps{Settings: stubSettings{}, TenantID: 1})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeSiteConfig(t, rec)

	if len(body) != expectedFieldCount {
		t.Fatalf("field count=%d want=%d body keys=%v", len(body), expectedFieldCount, keysOf(body))
	}
	// Discriminating defaults: each differs from the "broken" value a naive
	// implementation would produce (false/empty for everything).
	wantBool := map[string]bool{
		"registration_enabled":      false,
		"invitation_required":       true,
		"password_register_enabled": true,
		"password_login_enabled":    true,
		"captcha_enabled":           false,
		"two_factor_enabled":        true,
		"passkey_enabled":           false,
		"promo_enabled":             false,
	}
	for field, want := range wantBool {
		if got, ok := body[field].(bool); !ok || got != want {
			t.Fatalf("%s=%v (%T) want bool %v", field, body[field], body[field], want)
		}
	}
	wantString := map[string]string{
		"captcha_provider":        "",
		"captcha_site_key":        "",
		"passkey_rp_id":           "",
		"passkey_rp_display_name": "HUAKAI",
		"oauth_providers_enabled": "",
		"site_name":               "HUAKAI",
		"site_logo":               "",
		"site_footer":             "",
		"site_home_content":       "",
		"site_subtitle":           "",
		"site_contact_info":       "",
		"site_doc_url":            "",
		"site_api_base_url":       "",
	}
	for field, want := range wantString {
		if got, ok := body[field].(string); !ok || got != want {
			t.Fatalf("%s=%v (%T) want string %q", field, body[field], body[field], want)
		}
	}
	if tid, ok := body["tenant_id"].(float64); !ok || int64(tid) != 1 {
		t.Fatalf("tenant_id=%v (%T) want 1", body["tenant_id"], body["tenant_id"])
	}
}

// T2: DB-set values must override defaults AND booleans must be parsed into
// real JSON bools, not passed through as the string "true". Mutation: project
// the raw string instead of parsing -> body["registration_enabled"] becomes
// the string "true" and the bool assertion fails.
func TestSiteConfigAppliesDBValuesAndParsesBooleans(t *testing.T) {
	rec := serveSiteConfig(t, Deps{
		Settings: stubSettings{values: map[platformsettings.SettingKey]string{
			platformsettings.KeyRegistrationEnabled: "true",
			platformsettings.KeyCaptchaEnabled:      "true",
			platformsettings.KeyCaptchaProvider:     "turnstile",
			platformsettings.KeyCaptchaSiteKey:      "pk_pub123",
			platformsettings.KeySiteName:            "Acme Gateway",
		}},
		TenantID: 1,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeSiteConfig(t, rec)

	// JSON bool, not the string "true": a non-parsing handler returns a string
	// here and this type assertion fails.
	if v, ok := body["registration_enabled"].(bool); !ok || v != true {
		t.Fatalf("registration_enabled=%v (%T) want JSON bool true", body["registration_enabled"], body["registration_enabled"])
	}
	if v, ok := body["captcha_enabled"].(bool); !ok || v != true {
		t.Fatalf("captcha_enabled=%v (%T) want JSON bool true", body["captcha_enabled"], body["captcha_enabled"])
	}
	if body["captcha_provider"] != "turnstile" {
		t.Fatalf("captcha_provider=%v want turnstile", body["captcha_provider"])
	}
	// captcha_site_key is a PUBLIC key and must be projected verbatim.
	if body["captcha_site_key"] != "pk_pub123" {
		t.Fatalf("captcha_site_key=%v want pk_pub123 (public key must project)", body["captcha_site_key"])
	}
	if body["site_name"] != "Acme Gateway" {
		t.Fatalf("site_name=%v want Acme Gateway", body["site_name"])
	}
}

// T2b: the extended branding strings (subtitle/contact/doc URL/api base URL)
// must be projected verbatim from the store so an anonymous frontend can render
// them. Mutation: drop any of the four entries from the handler's stringKeys and
// that field falls back to its empty default here, failing the assertion.
func TestSiteConfigProjectsExtendedBranding(t *testing.T) {
	rec := serveSiteConfig(t, Deps{
		Settings: stubSettings{values: map[platformsettings.SettingKey]string{
			platformsettings.KeySiteSubtitle:    "Fast, clean gateway",
			platformsettings.KeySiteContactInfo: "ops@huakai.example",
			platformsettings.KeySiteDocURL:      "https://docs.huakai.example",
			platformsettings.KeySiteAPIBaseURL:  "https://api.huakai.example/v1",
		}},
		TenantID: 1,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeSiteConfig(t, rec)
	for field, want := range map[string]string{
		"site_subtitle":     "Fast, clean gateway",
		"site_contact_info": "ops@huakai.example",
		"site_doc_url":      "https://docs.huakai.example",
		"site_api_base_url": "https://api.huakai.example/v1",
	} {
		if got, ok := body[field].(string); !ok || got != want {
			t.Fatalf("%s=%v (%T) want %q", field, body[field], body[field], want)
		}
	}
}

// T3 (CMB-5 core): even when the settings store carries secret-bearing keys,
// the response must contain ONLY the allowlisted public fields and must not
// echo any secret substring. Mutation: add a line projecting
// KeyPaymentProviderConfig or KeyModerationExternalAPIKeys into the output map
// and BOTH the field-count assertion and the substring scan go red.
func TestSiteConfigNeverLeaksSecretBearingKeys(t *testing.T) {
	rec := serveSiteConfig(t, Deps{
		Settings: stubSettings{values: map[platformsettings.SettingKey]string{
			platformsettings.KeyPaymentProviderConfig:     `{"taobao":{"enabled":true,"checkout_url":"https://x"}}`,
			platformsettings.KeyModerationExternalAPIKeys: `["sk-LEAK"]`,
		}},
		TenantID: 1,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeSiteConfig(t, rec)

	if len(body) != expectedFieldCount {
		t.Fatalf("field count=%d want=%d (secret key leaked into projection?) keys=%v",
			len(body), expectedFieldCount, keysOf(body))
	}
	raw := rec.Body.String()
	for _, needle := range []string{
		"payment_provider", "moderation", "api_keys", "sk-LEAK", "checkout_url", "_secret",
	} {
		if strings.Contains(raw, needle) {
			t.Fatalf("response leaked secret-related substring %q: %s", needle, raw)
		}
	}
}

// T4: a turnstile secret living only in an env var must never appear in the
// response, because the handler reads the platform-settings allowlist and the
// secret env is not on it. Mutation: have the handler call os.Getenv on the
// turnstile secret and project it -> the substring scan goes red.
func TestSiteConfigDoesNotProjectTurnstileSecretEnv(t *testing.T) {
	t.Setenv("HUAKAI_CAPTCHA_TURNSTILE_SECRET", "secret-xyz")
	rec := serveSiteConfig(t, Deps{
		Settings: stubSettings{values: map[platformsettings.SettingKey]string{
			platformsettings.KeyCaptchaProvider: "turnstile",
			platformsettings.KeyCaptchaSiteKey:  "pk_pub123",
		}},
		TenantID: 1,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret-xyz") {
		t.Fatalf("response leaked turnstile secret env value: %s", rec.Body.String())
	}
}

// T5: a nil Settings dependency must degrade to a 503 with a stable error
// code, never a panic or a partially-formed body. Mutation: delete the nil
// guard in NewHandler and a nil-map Get panics / 500s instead.
func TestSiteConfigReturns503WhenSettingsUnset(t *testing.T) {
	rec := serveSiteConfig(t, Deps{Settings: nil, TenantID: 1})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503 body=%s", rec.Code, rec.Body.String())
	}
	body := decodeSiteConfig(t, rec)
	errObj, ok := body["error"].(map[string]any)
	if !ok || errObj["code"] != "gateway_not_configured" {
		t.Fatalf("body=%v want error.code=gateway_not_configured", body)
	}
}

// T6: tenant_id must be sourced from Deps, not hardcoded. Mutation: write a
// literal 0 (or 1) into the output map and a Deps{TenantID:7} request fails.
func TestSiteConfigInjectsTenantID(t *testing.T) {
	rec := serveSiteConfig(t, Deps{Settings: stubSettings{}, TenantID: 7})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeSiteConfig(t, rec)
	if tid, ok := body["tenant_id"].(float64); !ok || int64(tid) != 7 {
		t.Fatalf("tenant_id=%v (%T) want 7", body["tenant_id"], body["tenant_id"])
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
