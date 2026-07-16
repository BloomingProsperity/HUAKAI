package controlhttp

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
	"github.com/BloomingProsperity/HUAKAI/internal/admintest"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

func TestHandlerGETListTenantOperatorGets403(t *testing.T) {
	svc := &platformSettingsServiceStub{}
	handler := newPlatformSettingsTestRouter(PlatformSettingsDeps{
		Auth:    platformSettingsAuthStub{ident: admintest.TenantOperator(22, 7)},
		Service: svc,
	})

	rec := servePlatformSettingsJSON(t, handler, http.MethodGet, "/v1/admin/platform-settings/", nil)

	assertPlatformSettingsStatus(t, rec, http.StatusForbidden)
	assertPlatformSettingsErrorCode(t, rec, "admin_forbidden")
	if svc.listCalls != 0 {
		t.Fatalf("tenant operator reached service List %d times", svc.listCalls)
	}
}

func TestHandlerPUTTenantOperatorGets403(t *testing.T) {
	svc := &platformSettingsServiceStub{}
	handler := newPlatformSettingsTestRouter(PlatformSettingsDeps{
		Auth:    platformSettingsAuthStub{ident: admintest.TenantOperator(22, 7)},
		Service: svc,
	})

	rec := servePlatformSettingsJSON(t, handler, http.MethodPut, "/v1/admin/platform-settings/promo_enabled", map[string]any{
		"value": "true",
	})

	assertPlatformSettingsStatus(t, rec, http.StatusForbidden)
	assertPlatformSettingsErrorCode(t, rec, "admin_forbidden")
	if svc.upsertCalls != 0 {
		t.Fatalf("tenant operator reached service Upsert %d times", svc.upsertCalls)
	}
}

func TestHandlerGETSingleAbsentKeyReturnsDefault(t *testing.T) {
	svc := &platformSettingsServiceStub{
		getResult: platformsettings.StoredSetting{
			Key:    platformsettings.KeyRegistrationEnabled,
			Value:  "false",
			Source: platformsettings.SourceDefault,
		},
	}
	handler := newPlatformSettingsTestRouter(PlatformSettingsDeps{
		Auth:    platformSettingsAuthStub{ident: admintest.Platform(11)},
		Service: svc,
	})

	rec := servePlatformSettingsJSON(t, handler, http.MethodGet, "/v1/admin/platform-settings/registration_enabled", nil)

	assertPlatformSettingsStatus(t, rec, http.StatusOK)
	got := decodePlatformSettingsResponse(t, rec)
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
	svc := &platformSettingsServiceStub{}
	handler := newPlatformSettingsTestRouter(PlatformSettingsDeps{
		Auth:    platformSettingsAuthStub{ident: admintest.Platform(11)},
		Service: svc,
	})

	rec := servePlatformSettingsJSON(t, handler, http.MethodPut, "/v1/admin/platform-settings/smtp_password", map[string]any{
		"value": "do-not-store",
	})

	assertPlatformSettingsStatus(t, rec, http.StatusBadRequest)
	assertPlatformSettingsErrorCode(t, rec, "platform_setting_unknown_key")
	if svc.upsertCalls != 0 {
		t.Fatalf("unknown key reached service Upsert %d times", svc.upsertCalls)
	}
}

func TestHandlerPUTMissingValueFieldGets400(t *testing.T) {
	svc := &platformSettingsServiceStub{}
	handler := newPlatformSettingsTestRouter(PlatformSettingsDeps{
		Auth:    platformSettingsAuthStub{ident: admintest.Platform(11)},
		Service: svc,
	})

	rec := servePlatformSettingsJSON(t, handler, http.MethodPut, "/v1/admin/platform-settings/promo_enabled", map[string]any{
		"reason": "missing value",
	})

	assertPlatformSettingsStatus(t, rec, http.StatusBadRequest)
	assertPlatformSettingsErrorCode(t, rec, "platform_setting_value_required")
	if svc.upsertCalls != 0 {
		t.Fatalf("missing value reached service Upsert %d times", svc.upsertCalls)
	}
}

func TestHandlerPUTReasonOptionalWritesSetting(t *testing.T) {
	updatedAt := time.Date(2026, 6, 3, 5, 6, 7, 0, time.UTC)
	svc := &platformSettingsServiceStub{
		upsertResult: platformsettings.StoredSetting{
			Key:       platformsettings.KeyPromoEnabled,
			Value:     "true",
			Source:    platformsettings.SourceDB,
			UpdatedAt: updatedAt,
			UpdatedBy: "admin_token:11",
		},
	}
	handler := newPlatformSettingsTestRouter(PlatformSettingsDeps{
		Auth:    platformSettingsAuthStub{ident: admintest.Platform(11)},
		Service: svc,
	})

	rec := servePlatformSettingsJSON(t, handler, http.MethodPut, "/v1/admin/platform-settings/promo_enabled", map[string]any{
		"value": "true",
	})

	assertPlatformSettingsStatus(t, rec, http.StatusOK)
	if svc.upsertCalls != 1 {
		t.Fatalf("upsert calls=%d want 1", svc.upsertCalls)
	}
	if svc.lastUpsert.Key != platformsettings.KeyPromoEnabled || svc.lastUpsert.Value != "true" ||
		svc.lastUpsert.UpdatedBy != "admin_token:11" || svc.lastUpsert.ActorID != "admin_token:11" ||
		svc.lastUpsert.ActorRole != admin.RolePlatformAdmin || svc.lastUpsert.Reason != "" {
		t.Fatalf("upsert input=%+v", svc.lastUpsert)
	}
	got := decodePlatformSettingsResponse(t, rec)
	if got.Value != "true" || got.Source != "db" || got.UpdatedAt == nil || *got.UpdatedBy != "admin_token:11" {
		t.Fatalf("response=%+v want db true with metadata", got)
	}
}

// TestHandlerPUTCaptchaEnabledRequiresConfiguredSecret 守护这一配置期闸门:
// 运维可以在缺失 secret 的情况下启动,但在本网关进程配置好运行时 secret 之前,
// 不能开启 CAPTCHA。变异检查:删掉该守卫后,启用请求会到达 Upsert,而禁用请求
// 仍然证明这个检查不是一刀切的写入拦截器。
func TestHandlerPUTCaptchaEnabledRequiresConfiguredSecret(t *testing.T) {
	svc := &platformSettingsServiceStub{
		upsertResult: platformsettings.StoredSetting{
			Key:    platformsettings.KeyCaptchaEnabled,
			Value:  "false",
			Source: platformsettings.SourceDB,
		},
	}
	handler := newPlatformSettingsTestRouter(PlatformSettingsDeps{
		Auth:                    platformSettingsAuthStub{ident: admintest.Platform(11)},
		Service:                 svc,
		CaptchaSecretConfigured: func(context.Context) bool { return false },
	})

	rec := servePlatformSettingsJSON(t, handler, http.MethodPut, "/v1/admin/platform-settings/captcha_enabled", map[string]any{
		"value": "true",
	})

	assertPlatformSettingsStatus(t, rec, http.StatusBadRequest)
	assertPlatformSettingsErrorCode(t, rec, "captcha_secret_required")
	if svc.upsertCalls != 0 {
		t.Fatalf("missing secret enable reached Upsert %d times", svc.upsertCalls)
	}

	rec = servePlatformSettingsJSON(t, handler, http.MethodPut, "/v1/admin/platform-settings/captcha_enabled", map[string]any{
		"value": "false",
	})

	assertPlatformSettingsStatus(t, rec, http.StatusOK)
	if svc.upsertCalls != 1 || svc.lastUpsert.Key != platformsettings.KeyCaptchaEnabled || svc.lastUpsert.Value != "false" {
		t.Fatalf("disable upsert calls=%d input=%+v", svc.upsertCalls, svc.lastUpsert)
	}
}

// TestHandlerPUTCaptchaEnabledAllowedWhenSecretConfigured 是上面的判别镜像:secret 已配置
// (resolver 返回 true)时启用 captcha 必须放行到 Upsert。变异:若把配置门写死成恒 false
// (无视 resolver)→ 本用例会拿到 400 而非放行 → RED;证明 resolver 的 true 分支真被消费。
func TestHandlerPUTCaptchaEnabledAllowedWhenSecretConfigured(t *testing.T) {
	svc := &platformSettingsServiceStub{
		upsertResult: platformsettings.StoredSetting{
			Key:    platformsettings.KeyCaptchaEnabled,
			Value:  "true",
			Source: platformsettings.SourceDB,
		},
	}
	handler := newPlatformSettingsTestRouter(PlatformSettingsDeps{
		Auth:                    platformSettingsAuthStub{ident: admintest.Platform(11)},
		Service:                 svc,
		CaptchaSecretConfigured: func(context.Context) bool { return true },
	})

	rec := servePlatformSettingsJSON(t, handler, http.MethodPut, "/v1/admin/platform-settings/captcha_enabled", map[string]any{
		"value": "true",
	})

	assertPlatformSettingsStatus(t, rec, http.StatusOK)
	if svc.upsertCalls != 1 || svc.lastUpsert.Key != platformsettings.KeyCaptchaEnabled || svc.lastUpsert.Value != "true" {
		t.Fatalf("configured-secret enable upsert calls=%d input=%+v", svc.upsertCalls, svc.lastUpsert)
	}
}

// TestHandlerGETCaptchaEnabledShowsMissingSecretHealth 给管理员提供这个运行时
// 配置错误信号,它取代了原先的启动失败。变异检查:移除健康状态装饰后,响应仍会
// 返回该设置,但缺少降级的「缺失 secret」标记。
func TestHandlerGETCaptchaEnabledShowsMissingSecretHealth(t *testing.T) {
	svc := &platformSettingsServiceStub{
		getResult: platformsettings.StoredSetting{
			Key:    platformsettings.KeyCaptchaEnabled,
			Value:  "true",
			Source: platformsettings.SourceDB,
		},
	}
	handler := newPlatformSettingsTestRouter(PlatformSettingsDeps{
		Auth:                    platformSettingsAuthStub{ident: admintest.Platform(11)},
		Service:                 svc,
		CaptchaSecretConfigured: func(context.Context) bool { return false },
	})

	rec := servePlatformSettingsJSON(t, handler, http.MethodGet, "/v1/admin/platform-settings/captcha_enabled", nil)

	assertPlatformSettingsStatus(t, rec, http.StatusOK)
	got := decodePlatformSettingsResponse(t, rec)
	if got.Health == nil || got.Health.Status != "degraded" ||
		got.Health.Issue != "turnstile_secret_missing" || got.Health.CaptchaSecretConfigured {
		t.Fatalf("captcha health=%+v want degraded missing-secret marker", got.Health)
	}
}

func TestHandlerPUTLargeBodyRejectedWith413(t *testing.T) {
	svc := &platformSettingsServiceStub{}
	handler := newPlatformSettingsTestRouter(PlatformSettingsDeps{
		Auth:    platformSettingsAuthStub{ident: admintest.Platform(11)},
		Service: svc,
	})
	body := bytes.NewBufferString(`{"value":"` + strings.Repeat("x", 70<<10) + `"}`)
	req := httptest.NewRequest(http.MethodPut, "/v1/admin/platform-settings/promo_enabled", body)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assertPlatformSettingsStatus(t, rec, http.StatusRequestEntityTooLarge)
	assertPlatformSettingsErrorCode(t, rec, "body_too_large")
	if svc.upsertCalls != 0 {
		t.Fatalf("oversize body reached service Upsert %d times", svc.upsertCalls)
	}
}

func TestHandlerNilServiceReturns503(t *testing.T) {
	handler := newPlatformSettingsTestRouter(PlatformSettingsDeps{
		Auth: platformSettingsAuthStub{ident: admintest.Platform(11)},
	})

	rec := servePlatformSettingsJSON(t, handler, http.MethodGet, "/v1/admin/platform-settings/", nil)

	assertPlatformSettingsStatus(t, rec, http.StatusServiceUnavailable)
	assertPlatformSettingsErrorCode(t, rec, "gateway_not_configured")
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
	handler := newPlatformSettingsTestRouter(PlatformSettingsDeps{
		Auth: platformSettingsAuthStub{ident: admintest.Platform(11)},
		Service: &platformSettingsServiceStub{
			listResult: items,
		},
	})

	rec := servePlatformSettingsJSON(t, handler, http.MethodGet, "/v1/admin/platform-settings/", nil)

	assertPlatformSettingsStatus(t, rec, http.StatusOK)
	got := decodePlatformSettingsListResponse(t, rec)
	if len(got.Items) != len(platformsettings.AllKeys()) {
		t.Fatalf("items=%d want %d: %+v", len(got.Items), len(platformsettings.AllKeys()), got.Items)
	}
	seen := map[string]platformSettingsResponse{}
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

// canaryModerationAPIKey 是只在密钥类设置值里出现的判别性夹具串。脱敏正确时它
// 绝不应出现在任何读响应体中;一旦读路径回吐明文,断言会因为响应体含此子串而 RED。
const canaryModerationAPIKey = "sk-canary-moderation-7f3a9b2c"

// paymentCheckoutFragment 是塞进支付配置 checkout_url 里的判别性夹具串。payment 是
// 非密钥类配置(仅支付方式开关+收银台 URL),读路径应原样回吐、不脱敏,故它必须出现
// 在读响应体中;一旦 payment 被误当密钥脱敏,断言会因响应体缺此子串而 RED。
const paymentCheckoutFragment = "pay-checkout-d41d8cd9"

// TestHandlerGETModerationAPIKeysIsMaskedNotPlaintext 守护读路径对外部审核 provider
// bearer 密钥数组的脱敏:GET 单 key 时响应体不得含明文密钥子串,且必须以
// value_configured=true 指示已配置、Value 被清空。变异实验:删去
// platformSettingsResponseFromStored 中的脱敏分支(直接 Value: setting.Value),
// 明文密钥会回到响应体,本测试断言响应体不含 canary 子串处即 RED。
func TestHandlerGETModerationAPIKeysIsMaskedNotPlaintext(t *testing.T) {
	stored := `["` + canaryModerationAPIKey + `"]`
	svc := &platformSettingsServiceStub{
		getResult: platformsettings.StoredSetting{
			Key:    platformsettings.KeyModerationExternalAPIKeys,
			Value:  stored,
			Source: platformsettings.SourceDB,
		},
	}
	handler := newPlatformSettingsTestRouter(PlatformSettingsDeps{
		Auth:    platformSettingsAuthStub{ident: admintest.Platform(11)},
		Service: svc,
	})

	rec := servePlatformSettingsJSON(t, handler, http.MethodGet, "/v1/admin/platform-settings/moderation_external_api_keys", nil)

	assertPlatformSettingsStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), canaryModerationAPIKey) {
		t.Fatalf("响应体泄露了明文密钥: %s", rec.Body.String())
	}
	got := decodePlatformSettingsResponse(t, rec)
	if got.Value != "" {
		t.Fatalf("Value 应被清空, got=%q", got.Value)
	}
	if got.ValueConfigured == nil || !*got.ValueConfigured {
		t.Fatalf("已配置的密钥 value_configured 应为 true, got=%+v", got.ValueConfigured)
	}
}

// TestHandlerGETModerationAPIKeysEmptyShowsNotConfigured 守护“未配置”指示:空集合
// 占位 "[]" 应解读为未配置,value_configured=false。变异实验:把
// HasConfiguredSecretValue 对 "[]" 的判定改成返回 true,则空密钥会被错报为已配置,
// 本断言 RED。
func TestHandlerGETModerationAPIKeysEmptyShowsNotConfigured(t *testing.T) {
	svc := &platformSettingsServiceStub{
		getResult: platformsettings.StoredSetting{
			Key:    platformsettings.KeyModerationExternalAPIKeys,
			Value:  "[]",
			Source: platformsettings.SourceDefault,
		},
	}
	handler := newPlatformSettingsTestRouter(PlatformSettingsDeps{
		Auth:    platformSettingsAuthStub{ident: admintest.Platform(11)},
		Service: svc,
	})

	rec := servePlatformSettingsJSON(t, handler, http.MethodGet, "/v1/admin/platform-settings/moderation_external_api_keys", nil)

	assertPlatformSettingsStatus(t, rec, http.StatusOK)
	got := decodePlatformSettingsResponse(t, rec)
	if got.ValueConfigured == nil || *got.ValueConfigured {
		t.Fatalf("空密钥 value_configured 应为 false, got=%+v", got.ValueConfigured)
	}
	if got.Value != "" {
		t.Fatalf("Value 应被清空, got=%q", got.Value)
	}
}

// TestHandlerGETListMasksSecretKeysButNotPublicKeys 是自证测试:List 同时返回密钥类
// 与非密钥类 key,断言唯一密钥类(moderation 数组)的明文不在响应体,而非密钥类的
// payment 配置(支付方式开关+收银台 URL)与 site_name 明文原样保留。既证脱敏生效,
// 又证未误伤非密钥配置。变异实验一:删脱敏分支 → moderation 明文回到响应体 → 首个
// 断言 RED。变异实验二:把脱敏判定改成无条件脱敏、或把 payment 误加进 secretSettingKeys
// → payment/site_name 明文被清空 → 对应"明文应保留"断言 RED。
func TestHandlerGETListMasksSecretKeysButNotPublicKeys(t *testing.T) {
	const siteNamePlain = "华楷中转站"
	items := []platformsettings.StoredSetting{
		{
			Key:    platformsettings.KeyModerationExternalAPIKeys,
			Value:  `["` + canaryModerationAPIKey + `"]`,
			Source: platformsettings.SourceDB,
		},
		{
			Key:    platformsettings.KeyPaymentProviderConfig,
			Value:  `{"manual":{"enabled":true,"checkout_url":""},"taobao":{"enabled":true,"checkout_url":"https://pay.example/` + paymentCheckoutFragment + `"}}`,
			Source: platformsettings.SourceDB,
		},
		{
			Key:    platformsettings.KeySiteName,
			Value:  siteNamePlain,
			Source: platformsettings.SourceDB,
		},
	}
	handler := newPlatformSettingsTestRouter(PlatformSettingsDeps{
		Auth:    platformSettingsAuthStub{ident: admintest.Platform(11)},
		Service: &platformSettingsServiceStub{listResult: items},
	})

	rec := servePlatformSettingsJSON(t, handler, http.MethodGet, "/v1/admin/platform-settings/", nil)

	assertPlatformSettingsStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	if strings.Contains(body, canaryModerationAPIKey) {
		t.Fatalf("List 泄露了 moderation 明文密钥: %s", body)
	}
	if !strings.Contains(body, paymentCheckoutFragment) {
		t.Fatalf("payment 是非密钥配置,读路径应原样返回其 checkout_url,却未出现在响应体: %s", body)
	}
	if !strings.Contains(body, siteNamePlain) {
		t.Fatalf("非密钥类 site_name 明文被误伤删除: %s", body)
	}

	got := decodePlatformSettingsListResponse(t, rec)
	seen := map[string]platformSettingsResponse{}
	for _, item := range got.Items {
		seen[item.Key] = item
	}
	mod := seen["moderation_external_api_keys"]
	if mod.Value != "" || mod.ValueConfigured == nil || !*mod.ValueConfigured {
		t.Fatalf("moderation 项未正确脱敏: %+v", mod)
	}
	pay := seen["payment_provider_config"]
	if pay.ValueConfigured != nil || !strings.Contains(pay.Value, paymentCheckoutFragment) {
		t.Fatalf("payment 是非密钥配置,应原样返回(Value 含 checkout_url、不带 value_configured): %+v", pay)
	}
	site := seen["site_name"]
	if site.Value != siteNamePlain || site.ValueConfigured != nil {
		t.Fatalf("非密钥类 site_name 不应脱敏: %+v", site)
	}
}

type platformSettingsAuthStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s platformSettingsAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	if s.err != nil {
		return admin.AdminIdentity{}, s.err
	}
	return s.ident, nil
}

type platformSettingsServiceStub struct {
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

func (s *platformSettingsServiceStub) Get(_ context.Context, key platformsettings.SettingKey) (platformsettings.StoredSetting, error) {
	s.getCalls++
	s.lastGetKey = key
	if s.err != nil {
		return platformsettings.StoredSetting{}, s.err
	}
	return s.getResult, nil
}

func (s *platformSettingsServiceStub) List(context.Context) ([]platformsettings.StoredSetting, error) {
	s.listCalls++
	if s.err != nil {
		return nil, s.err
	}
	return append([]platformsettings.StoredSetting(nil), s.listResult...), nil
}

func (s *platformSettingsServiceStub) Upsert(_ context.Context, in platformsettings.UpsertInput) (platformsettings.StoredSetting, error) {
	s.upsertCalls++
	s.lastUpsert = in
	if s.err != nil {
		return platformsettings.StoredSetting{}, s.err
	}
	return s.upsertResult, nil
}

func newPlatformSettingsTestRouter(d PlatformSettingsDeps) http.Handler {
	r := chi.NewRouter()
	r.Route("/v1/admin/platform-settings", func(r chi.Router) {
		MountPlatformSettingsRoutes(r, d)
	})
	return r
}

func servePlatformSettingsJSON(t *testing.T, handler http.Handler, method, path string, body any) *httptest.ResponseRecorder {
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

func assertPlatformSettingsStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want %d body=%s", rec.Code, want, rec.Body.String())
	}
}

func assertPlatformSettingsErrorCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
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

func decodePlatformSettingsResponse(t *testing.T, rec *httptest.ResponseRecorder) platformSettingsResponse {
	t.Helper()
	var body platformSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode setting response: %v body=%s", err, rec.Body.String())
	}
	return body
}

func decodePlatformSettingsListResponse(t *testing.T, rec *httptest.ResponseRecorder) platformSettingsListResponse {
	t.Helper()
	var body platformSettingsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list response: %v body=%s", err, rec.Body.String())
	}
	return body
}

var _ PlatformSettingsAuth = platformSettingsAuthStub{}
var _ PlatformSettingsService = (*platformSettingsServiceStub)(nil)
var _ = errors.Is
