package platformsettings

import (
	"errors"
	"strings"
	"testing"
)

func TestPasskeySettingsAreAllowListedAndFailClosedByDefault(t *testing.T) {
	// 已消除变异：把某个 passkey key 从 orderedSettingKeys 里漏掉会让它在
	// admin 设置里不可见，而默认 true 又会在 RP 配置完成之前就启用鉴权变更。
	for _, key := range []SettingKey{
		KeyPasskeyEnabled,
		KeyPasskeyRegistrationEnabled,
		KeyPasskeyRPID,
		KeyPasskeyRPDisplayName,
		KeyPasskeyRPOrigins,
	} {
		if !IsAllowedKey(key) {
			t.Fatalf("%s is not allow-listed", key)
		}
		if _, err := ValidateValue(key, defaultSettingValueMap[key]); err != nil {
			t.Fatalf("default %s=%q invalid: %v", key, defaultSettingValueMap[key], err)
		}
	}
	if defaultSettingValueMap[KeyPasskeyEnabled] != "false" || defaultSettingValueMap[KeyPasskeyRegistrationEnabled] != "false" {
		t.Fatalf("passkey defaults must fail closed: enabled=%q registration=%q",
			defaultSettingValueMap[KeyPasskeyEnabled], defaultSettingValueMap[KeyPasskeyRegistrationEnabled])
	}
}

func TestPasskeyRPOriginsMustBeJSONStringArray(t *testing.T) {
	// 已消除变异：接受纯文本 origin 会让 origin 白名单的解析产生歧义，可能误把
	// 请求可控的输入授权放行。
	valid := `["https://app.example.test","https://admin.example.test"]`
	if got, err := ValidateValue(KeyPasskeyRPOrigins, valid); err != nil || got != valid {
		t.Fatalf("ValidateValue valid origins got=%q err=%v", got, err)
	}
	for _, bad := range []string{`https://app.example.test`, `{}`, `[""]`, `["ok", ""]`} {
		if _, err := ValidateValue(KeyPasskeyRPOrigins, bad); !errors.Is(err, ErrInvalidValue) {
			t.Fatalf("ValidateValue(%q) err=%v want ErrInvalidValue", bad, err)
		}
	}
	if strings.TrimSpace(defaultSettingValueMap[KeyPasskeyRPOrigins]) != "[]" {
		t.Fatalf("passkey_rp_origins default=%q want []", defaultSettingValueMap[KeyPasskeyRPOrigins])
	}
}
