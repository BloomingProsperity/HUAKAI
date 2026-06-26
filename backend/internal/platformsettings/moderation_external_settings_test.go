package platformsettings

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestModerationExternalSettingsDefaultOffAndValidate(t *testing.T) {
	// 变异:从白名单中漏掉任一 key 会使其在 admin 设置中不可见;
	// 把 enabled/image_enabled 默认成 true 会在上线前改变运行时行为。
	expectedDefaults := map[SettingKey]string{
		KeyModerationExternalEnabled:      "false",
		KeyModerationExternalBaseURL:      "",
		KeyModerationExternalAPIKeys:      "[]",
		KeyModerationExternalModel:        "omni-moderation-latest",
		KeyModerationExternalThresholds:   "{}",
		KeyModerationExternalTimeoutMS:    "3000",
		KeyModerationExternalRetryCount:   "2",
		KeyModerationExternalImageEnabled: "false",
	}
	for key, want := range expectedDefaults {
		if !IsAllowedKey(key) {
			t.Fatalf("%s is not allow-listed", key)
		}
		got, ok := DefaultValue(key)
		if !ok || got != want {
			t.Fatalf("default %s=%q ok=%v want %q", key, got, ok, want)
		}
		if _, err := ValidateValue(key, got); err != nil {
			t.Fatalf("default %s=%q invalid: %v", key, got, err)
		}
	}
}

func TestModerationExternalSettingsRejectInvalidValues(t *testing.T) {
	cases := []struct {
		name  string
		key   SettingKey
		value string
	}{
		{name: "bad_url_scheme", key: KeyModerationExternalBaseURL, value: "file:///etc/passwd"},
		{name: "api_keys_not_array", key: KeyModerationExternalAPIKeys, value: `{"key":"secret"}`},
		{name: "api_keys_empty_item", key: KeyModerationExternalAPIKeys, value: `["valid",""]`},
		{name: "thresholds_not_object", key: KeyModerationExternalThresholds, value: `["violence"]`},
		{name: "threshold_negative", key: KeyModerationExternalThresholds, value: `{"violence":-0.1}`},
		{name: "threshold_above_one", key: KeyModerationExternalThresholds, value: `{"violence":1.1}`},
		{name: "timeout_negative", key: KeyModerationExternalTimeoutMS, value: "-1"},
		{name: "timeout_above_max", key: KeyModerationExternalTimeoutMS, value: "30001"},
		{name: "retry_negative", key: KeyModerationExternalRetryCount, value: "-1"},
		{name: "retry_above_max", key: KeyModerationExternalRetryCount, value: "6"},
		{name: "image_enabled_not_bool", key: KeyModerationExternalImageEnabled, value: "yes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateValue(tc.key, tc.value); !errors.Is(err, ErrInvalidValue) {
				t.Fatalf("ValidateValue(%s, %q) err=%v want ErrInvalidValue", tc.key, tc.value, err)
			}
		})
	}
}

func TestModerationExternalSettingsAcceptValidValues(t *testing.T) {
	validThresholds := `{"self-harm":0.42,"violence":0.73}`
	gotThresholds, err := ValidateValue(KeyModerationExternalThresholds, validThresholds)
	if err != nil {
		t.Fatalf("ValidateValue thresholds: %v", err)
	}
	var decoded map[string]float64
	if err := json.Unmarshal([]byte(gotThresholds), &decoded); err != nil {
		t.Fatalf("threshold JSON invalid after validation: %v", err)
	}
	if decoded["violence"] != 0.73 || decoded["self-harm"] != 0.42 {
		t.Fatalf("thresholds decoded=%v want violence/self-harm values", decoded)
	}
	for key, value := range map[SettingKey]string{
		KeyModerationExternalEnabled:      "true",
		KeyModerationExternalBaseURL:      "https://moderation.example.test/v1/moderations",
		KeyModerationExternalAPIKeys:      `["sk-test-a","sk-test-b"]`,
		KeyModerationExternalModel:        "omni-moderation-latest",
		KeyModerationExternalTimeoutMS:    "30000",
		KeyModerationExternalRetryCount:   "5",
		KeyModerationExternalImageEnabled: "true",
	} {
		if _, err := ValidateValue(key, value); err != nil {
			t.Fatalf("ValidateValue(%s, %q): %v", key, value, err)
		}
	}
}

func TestModerationExternalAPIKeysRedactedFromAuditPayload(t *testing.T) {
	// 变异:直接序列化旧值/新值会把 provider API key 泄漏进 admin 审计载荷,
	// 使本测试变红。
	payload, err := platformSettingAuditPayload(AuditParams{
		Key:       KeyModerationExternalAPIKeys,
		OldValue:  `["sk-old-secret"]`,
		OldSource: SourceDB,
		NewValue:  `["sk-new-secret"]`,
	})
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	raw := string(payload)
	if strings.Contains(raw, "sk-old-secret") || strings.Contains(raw, "sk-new-secret") {
		t.Fatalf("audit payload leaked moderation API key: %s", raw)
	}
	if !strings.Contains(raw, "[redacted]") {
		t.Fatalf("audit payload=%s want redacted marker", raw)
	}
}
