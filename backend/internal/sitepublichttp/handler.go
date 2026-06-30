// Package sitepublichttp 提供匿名的站点引导配置 endpoint。
//
// GET /v1/site/config 是未登录前端调用的免鉴权 endpoint,用于获知站点品牌
//(name/logo/footer)以及该渲染哪些 auth/注册相关能力(注册开关、captcha
// 公钥、passkey relying-party 提示、oauth provider 列表)。
//
// 披露纪律:本 handler 只投射一份显式的公开 setting key allowlist。带密钥的 key
//(payment provider 配置、moderation api keys、任何 *_secret env)在此从不读取,
// 因此即便出错也不会泄漏密钥——这个投射是 allowlist,而非对全量 settings 面的 denylist。
package sitepublichttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

// Settings 是本 handler 所需的 platformsettings.Service 只读切面。Get 由缓存支撑,
// 在 store 出现瞬时错误时回退到最近一次已知值/默认值,因此 endpoint 仍以 200 返回
// 安全默认值。
type Settings interface {
	Get(context.Context, platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}

// Deps 承载 handler 的协作者。在 gateway 完成 wiring 之前 Settings 可能为 nil;
// 此时 handler 降级为 503 而非 panic。
type Deps struct {
	Settings Settings
	TenantID int64
}

// boolKeys 是以 JSON 布尔值投射的公开开关。其存储的字符串值通过
// TrimSpace == "true" 解析。
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

// stringKeys 是原样投射的公开字符串值。这里的每一个 key 都是公开值(provider 名称、
// 公开的 captcha site key、relying-party id/display name、oauth provider 列表、品牌字符串)
// ——没有一个是密钥。
var stringKeys = []struct {
	field string
	key   platformsettings.SettingKey
}{
	{"captcha_provider", platformsettings.KeyCaptchaProvider},
	{"captcha_site_key", platformsettings.KeyCaptchaSiteKey},
	{"passkey_rp_id", platformsettings.KeyPasskeyRPID},
	{"passkey_rp_display_name", platformsettings.KeyPasskeyRPDisplayName},
	{"oauth_providers_enabled", platformsettings.KeyOAuthProvidersEnabled},
	// telegram_bot_username 是 Telegram Login Widget 渲染所需的公开 bot 用户名(t.me/<name>),
	// 非密钥。bot token 是密钥,只走 env,从不进入本投射面。
	{"telegram_bot_username", platformsettings.KeyTelegramBotUsername},
	{"site_name", platformsettings.KeySiteName},
	{"site_logo", platformsettings.KeySiteLogo},
	{"site_footer", platformsettings.KeySiteFooter},
	{"site_home_content", platformsettings.KeySiteHomeContent},
	{"site_subtitle", platformsettings.KeySiteSubtitle},
	{"site_contact_info", platformsettings.KeySiteContactInfo},
	{"site_doc_url", platformsettings.KeySiteDocURL},
	{"site_api_base_url", platformsettings.KeySiteAPIBaseURL},
}

// NewHandler 返回 GET /v1/site/config 的 handler。它是匿名的:无 auth middleware、
// 无 request body、无 query 参数。全局 IP 限流在 router 层生效。
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

// settingString 把单个公开 setting 解析为其字符串值,当 service 或 lookup 没有返回
// 任何内容时回退到编译期默认值。
func settingString(ctx context.Context, settings Settings, key platformsettings.SettingKey) string {
	value, _ := platformsettings.DefaultValue(key)
	if setting, err := settings.Get(ctx, key); err == nil {
		value = setting.Value
	}
	return value
}

// settingTrue 把公开开关解析为 bool。其存储表示是字符串 "true"/"false";
// 其它任何值(包括缺失的默认值)都读作 false。
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
