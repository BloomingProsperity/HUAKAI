package platformsettings

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestIsSecretKeyClassifiesCredentialKeys 守护密钥类 key 的分类:外部审核 bearer
// 密钥数组必须判为密钥类;支付 provider 配置(仅支付方式开关+收银台 URL,schema 封闭
// 无凭据)与公开展示类 key(site_name)都不得被误判为密钥类。变异实验:从
// secretSettingKeys 删去 KeyModerationExternalAPIKeys,则首个断言 RED;把
// KeyPaymentProviderConfig 或 KeySiteName 误加进去,则对应"不应判为密钥类"断言 RED。
func TestIsSecretKeyClassifiesCredentialKeys(t *testing.T) {
	if !IsSecretKey(KeyModerationExternalAPIKeys) {
		t.Fatalf("moderation_external_api_keys 应判为密钥类")
	}
	if IsSecretKey(KeyPaymentProviderConfig) {
		t.Fatalf("payment_provider_config 不应判为密钥类(仅支付方式开关+收银台 URL,非凭据)")
	}
	if IsSecretKey(KeySiteName) {
		t.Fatalf("site_name 不应判为密钥类")
	}
}

// TestHasConfiguredSecretValueTreatsEmptyContainersAsUnset 守护“已配置”判定:空串、
// 空数组 "[]"、空对象 "{}" 都算未配置,只有真正非空值才算已配置。变异实验:把
// 空容器分支去掉(只判 ""),则 "[]" 会被错报为已配置,对应断言 RED。
func TestHasConfiguredSecretValueTreatsEmptyContainersAsUnset(t *testing.T) {
	unset := []string{"", "  ", "[]", "{}"}
	for _, v := range unset {
		if HasConfiguredSecretValue(KeyModerationExternalAPIKeys, v) {
			t.Fatalf("空值 %q 应判为未配置", v)
		}
	}
	if !HasConfiguredSecretValue(KeyModerationExternalAPIKeys, `["sk-real-key"]`) {
		t.Fatalf("非空密钥数组应判为已配置")
	}
	// 非密钥类 key 永远返回 false(它们不走脱敏指示)。
	if HasConfiguredSecretValue(KeySiteName, "HUAKAI") {
		t.Fatalf("非密钥类 key 不应报告 configured")
	}
}

// TestAuditPayloadRedactsAllSecretKeys 守护审计脱敏只覆盖真凭据类 key:moderation
// 密钥明文不得出现在审计 payload 中;而非密钥类的 payment 配置(支付方式开关+收银台
// URL)与 site_name 明文照常保留(支付配置变更要可审计、可追溯,不该被脱敏埋没)。
// 变异实验:把 payment 误加进 secretSettingKeys,则 payment 配置明文从 payload 消失,
// 对应"payment 配置应进入 payload"断言 RED;删 moderation 脱敏则首个断言 RED。
func TestAuditPayloadRedactsAllSecretKeys(t *testing.T) {
	const moderationSecret = "sk-canary-mod-abc123"
	const paymentCheckoutFragment = "pay-checkout-xyz789"

	payload, err := platformSettingAuditPayload(AuditParams{
		Key:      KeyModerationExternalAPIKeys,
		OldValue: `["` + moderationSecret + `"]`,
		NewValue: `["` + moderationSecret + `","second"]`,
	})
	if err != nil {
		t.Fatalf("build moderation payload: %v", err)
	}
	if strings.Contains(string(payload), moderationSecret) {
		t.Fatalf("审计 payload 泄露 moderation 明文: %s", payload)
	}

	// payment 配置是非密钥类:其收银台 URL 等明文应原样进入审计 payload(可追溯),不被脱敏。
	payload, err = platformSettingAuditPayload(AuditParams{
		Key:      KeyPaymentProviderConfig,
		OldValue: `{"manual":{"enabled":true,"checkout_url":""},"taobao":{"enabled":true,"checkout_url":"https://pay.example/` + paymentCheckoutFragment + `"}}`,
		NewValue: `{"manual":{"enabled":true,"checkout_url":""},"taobao":{"enabled":false,"checkout_url":""}}`,
	})
	if err != nil {
		t.Fatalf("build payment payload: %v", err)
	}
	if !strings.Contains(string(payload), paymentCheckoutFragment) {
		t.Fatalf("payment 配置(非密钥类)应进入审计 payload 以便追溯,却被脱敏: %s", payload)
	}

	// 非密钥类 site_name 明文应原样进入 payload(不误伤)。
	payload, err = platformSettingAuditPayload(AuditParams{
		Key:      KeySiteName,
		OldValue: "旧站名",
		NewValue: "新站名",
	})
	if err != nil {
		t.Fatalf("build site_name payload: %v", err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode site_name payload: %v", err)
	}
	if decoded["new_value"] != "新站名" {
		t.Fatalf("非密钥类 site_name 不应被脱敏: %+v", decoded)
	}
}
