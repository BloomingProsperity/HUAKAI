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

// expectedFieldCount 是公开投射的精确尺寸:tenant_id + 8 个布尔 + 14 个字符串
// (14 含新增的 telegram_bot_username 公开 bot 用户名)。
// 它被直接断言,这样在 handler 加了 key 却没更新本测试(或反之)时就能被抓到,
// 也让 CMB-5 泄漏测试能够证明「没有多余的 key 混进来」。
const expectedFieldCount = 1 + 8 + 14

// stubSettings 提供预设的 setting 值;缺失的 key 回退到编译期默认值,
// 与真实 Service 在 DB-miss 时的行为完全一致。
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

// T1:使用空 stub 时,每个字段都必须等于编译期默认值。防止漏掉某个投射 key 或
// 默认值错误。变异:把 types.go 里 KeyTwoFactorEnabled 的默认值翻成 "false",本测试变红。
func TestSiteConfigProjectsCompiledDefaults(t *testing.T) {
	rec := serveSiteConfig(t, Deps{Settings: stubSettings{}, TenantID: 1})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeSiteConfig(t, rec)

	if len(body) != expectedFieldCount {
		t.Fatalf("field count=%d want=%d body keys=%v", len(body), expectedFieldCount, keysOf(body))
	}
	// 判别性默认值:每一个都不同于幼稚实现会产出的「坏」值(对所有项都是 false/空)。
	wantBool := map[string]bool{
		"registration_enabled":      false,
		"invitation_required":       true,
		"password_register_enabled": true,
		"password_login_enabled":    true,
		"captcha_enabled":           false,
		"two_factor_enabled":        true,
		"passkey_enabled":           false,
		// promo_enabled 默认翻转 false→true(Owner A 方案:行为保持 + 一个能用的开关)。
		"promo_enabled": true,
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
		"telegram_bot_username":   "",
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

// T2:DB 设置的值必须覆盖默认值,且布尔值必须被解析为真正的 JSON bool,
// 而不是原样以字符串 "true" 透传。变异:投射原始字符串而不解析 ->
// body["registration_enabled"] 变成字符串 "true",bool 断言失败。
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

	// 是 JSON bool 而非字符串 "true":不做解析的 handler 在此返回字符串,
	// 这个类型断言就会失败。
	if v, ok := body["registration_enabled"].(bool); !ok || v != true {
		t.Fatalf("registration_enabled=%v (%T) want JSON bool true", body["registration_enabled"], body["registration_enabled"])
	}
	if v, ok := body["captcha_enabled"].(bool); !ok || v != true {
		t.Fatalf("captcha_enabled=%v (%T) want JSON bool true", body["captcha_enabled"], body["captcha_enabled"])
	}
	if body["captcha_provider"] != "turnstile" {
		t.Fatalf("captcha_provider=%v want turnstile", body["captcha_provider"])
	}
	// captcha_site_key 是公开 key,必须原样投射。
	if body["captcha_site_key"] != "pk_pub123" {
		t.Fatalf("captcha_site_key=%v want pk_pub123 (public key must project)", body["captcha_site_key"])
	}
	if body["site_name"] != "Acme Gateway" {
		t.Fatalf("site_name=%v want Acme Gateway", body["site_name"])
	}
}

// T2b:扩展品牌字符串(subtitle/contact/doc URL/api base URL)必须从 store 原样投射,
// 以便匿名前端能渲染它们。变异:从 handler 的 stringKeys 删掉这四项中任何一项,该字段
// 在此就会回退到它的空默认值,断言失败。
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

// T2c:公开 Telegram bot 用户名必须从 store 原样投射,匿名前端才能据此渲染 Login Widget。
// 变异:从 handler 的 stringKeys 删掉 telegram_bot_username 这一项,该字段回退到空默认值,
// 断言失败。这是 telegram 登录前端能接线的前提(前端拿不到 username 就渲染不出 widget)。
func TestSiteConfigProjectsTelegramBotUsername(t *testing.T) {
	rec := serveSiteConfig(t, Deps{
		Settings: stubSettings{values: map[platformsettings.SettingKey]string{
			platformsettings.KeyTelegramBotUsername: "HuakaiLoginBot",
		}},
		TenantID: 1,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeSiteConfig(t, rec)
	if got, ok := body["telegram_bot_username"].(string); !ok || got != "HuakaiLoginBot" {
		t.Fatalf("telegram_bot_username=%v (%T) want HuakaiLoginBot", body["telegram_bot_username"], body["telegram_bot_username"])
	}
}

// T3(CMB-5 核心):即便 settings store 携带带密钥的 key,响应也只能包含 allowlist 中的
// 公开字段,且绝不能回显任何密钥子串。变异:加一行把 KeyPaymentProviderConfig 或
// KeyModerationExternalAPIKeys 投射进输出 map,字段数断言与子串扫描都会变红。
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

// T4:只存在于 env 变量里的 turnstile secret 绝不能出现在响应中,因为 handler 读的是
// platform-settings 的 allowlist,而 secret env 不在其中。变异:让 handler 对 turnstile
// secret 调 os.Getenv 并投射它 -> 子串扫描变红。
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

// T5:nil 的 Settings 依赖必须降级为 503 并带稳定的 error code,绝不能 panic 或返回
// 半成型的 body。变异:删掉 NewHandler 里的 nil 守卫,nil-map 的 Get 会 panic / 500。
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

// T6:tenant_id 必须来源于 Deps,而非硬编码。变异:在输出 map 里写死字面量 0(或 1),
// 则 Deps{TenantID:7} 的请求会失败。
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
