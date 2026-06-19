package platformsettings

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestIsSecretKeyClassifiesCredentialKeys 守护密钥类 key 的分类:外部审核 bearer
// 密钥数组与支付 provider 配置必须判为密钥类,而典型公开展示类 key(site_name)
// 不得被误判。变异实验:从 secretSettingKeys 删去 KeyModerationExternalAPIKeys,
// 则首个断言 RED;把 KeySiteName 误加进去,则最后一个断言 RED。
func TestIsSecretKeyClassifiesCredentialKeys(t *testing.T) {
	if !IsSecretKey(KeyModerationExternalAPIKeys) {
		t.Fatalf("moderation_external_api_keys 应判为密钥类")
	}
	if !IsSecretKey(KeyPaymentProviderConfig) {
		t.Fatalf("payment_provider_config 应判为密钥类")
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

// TestAuditPayloadRedactsAllSecretKeys 守护审计脱敏与读路径共用同一份密钥清单:
// moderation 密钥与 payment 配置的明文都不得出现在审计 payload 中,而非密钥类的
// site_name 明文照常保留。变异实验:把 auditValueForSetting 的 IsSecretKey 改回只
// 判 KeyModerationExternalAPIKeys,则 payment 明文会回到 payload,对应断言 RED。
func TestAuditPayloadRedactsAllSecretKeys(t *testing.T) {
	const moderationSecret = "sk-canary-mod-abc123"
	const paymentSecret = "pay-canary-xyz789"

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

	payload, err = platformSettingAuditPayload(AuditParams{
		Key:      KeyPaymentProviderConfig,
		OldValue: `{"taobao":{"enabled":true,"checkout_url":"https://x/` + paymentSecret + `"}}`,
		NewValue: `{"taobao":{"enabled":false,"checkout_url":""}}`,
	})
	if err != nil {
		t.Fatalf("build payment payload: %v", err)
	}
	if strings.Contains(string(payload), paymentSecret) {
		t.Fatalf("审计 payload 泄露 payment 明文: %s", payload)
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
