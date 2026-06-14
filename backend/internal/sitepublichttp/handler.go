// Package sitepublichttp serves the anonymous site-bootstrap config endpoint.
//
// GET /v1/site/config is the unauthenticated endpoint a not-yet-logged-in
// frontend calls to learn the site's brand (name/logo/footer) and which
// auth/registration affordances to render (registration toggle, captcha
// public key, passkey relying-party hints, oauth provider list).
//
// Disclosure discipline: this handler projects an explicit allowlist of
// public setting keys only. Secret-bearing keys (payment provider config,
// moderation api keys, any *_secret env) are never read here, so a secret
// cannot leak even by accident — the projection is an allowlist, not a
// denylist over the full settings surface.
package sitepublichttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

// Settings is the read-only slice of platformsettings.Service this handler
// needs. Get is cache-backed and falls back to last-known/default on a
// transient store error, so the endpoint stays 200 with safe defaults.
type Settings interface {
	Get(context.Context, platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}

// Deps carries the handler's collaborators. Settings may be nil before the
// gateway finishes wiring; the handler degrades to 503 rather than panic.
type Deps struct {
	Settings Settings
	TenantID int64
}

// boolKeys are the public toggles projected as JSON booleans. Their stored
// string value is parsed via TrimSpace == "true".
var boolKeys = []struct {
	field string
	key   platformsettings.SettingKey
}{
	{"registration_enabled", platformsettings.KeyRegistrationEnabled},
	{"invitation_required", platformsettings.KeyInvitationRequired},
	{"password_register_enabled", platformsettings.KeyPasswordRegisterEnabled},
	{"password_login_enabled", platformsettings.KeyPasswordLoginEnabled},
	{"captcha_enabled", platformsettings.KeyCaptchaEnabled},
	{"two_factor_enabled", platformsettings.KeyTwoFactorEnabled},
	{"passkey_enabled", platformsettings.KeyPasskeyEnabled},
	{"promo_enabled", platformsettings.KeyPromoEnabled},
}

// stringKeys are the public string values projected verbatim. Every key here
// is a public value (provider name, public captcha site key, relying-party
// id/display name, oauth provider list, brand strings) — none is a secret.
var stringKeys = []struct {
	field string
	key   platformsettings.SettingKey
}{
	{"captcha_provider", platformsettings.KeyCaptchaProvider},
	{"captcha_site_key", platformsettings.KeyCaptchaSiteKey},
	{"passkey_rp_id", platformsettings.KeyPasskeyRPID},
	{"passkey_rp_display_name", platformsettings.KeyPasskeyRPDisplayName},
	{"oauth_providers_enabled", platformsettings.KeyOAuthProvidersEnabled},
	{"site_name", platformsettings.KeySiteName},
	{"site_logo", platformsettings.KeySiteLogo},
	{"site_footer", platformsettings.KeySiteFooter},
	{"site_home_content", platformsettings.KeySiteHomeContent},
	{"site_subtitle", platformsettings.KeySiteSubtitle},
	{"site_contact_info", platformsettings.KeySiteContactInfo},
	{"site_doc_url", platformsettings.KeySiteDocURL},
	{"site_api_base_url", platformsettings.KeySiteAPIBaseURL},
}

// NewHandler returns the GET /v1/site/config handler. It is anonymous: no auth
// middleware, no request body, no query parameters. The global IP rate limiter
// applies at the router level.
func NewHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Settings == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "site config dependency unset")
			return
		}
		ctx := r.Context()
		out := make(map[string]any, 1+len(boolKeys)+len(stringKeys))
		out["tenant_id"] = d.TenantID
		for _, b := range boolKeys {
			out[b.field] = settingTrue(ctx, d.Settings, b.key)
		}
		for _, s := range stringKeys {
			out[s.field] = settingString(ctx, d.Settings, s.key)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// settingString resolves a single public setting to its string value, falling
// back to the compiled default when the service or lookup yields nothing.
func settingString(ctx context.Context, settings Settings, key platformsettings.SettingKey) string {
	value, _ := platformsettings.DefaultValue(key)
	if setting, err := settings.Get(ctx, key); err == nil {
		value = setting.Value
	}
	return value
}

// settingTrue resolves a public toggle to a bool. The stored representation is
// the string "true"/"false"; anything else (including a missing default)
// reads as false.
func settingTrue(ctx context.Context, settings Settings, key platformsettings.SettingKey) bool {
	return strings.TrimSpace(settingString(ctx, settings, key)) == "true"
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]map[string]string{
		"error": {"code": code, "message": message},
	})
}
