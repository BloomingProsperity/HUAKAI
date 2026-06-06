package platformsettings

import (
	"errors"
	"strings"
	"testing"
)

func TestPasskeySettingsAreAllowListedAndFailClosedByDefault(t *testing.T) {
	// Mutation killed: omitting a passkey key from orderedSettingKeys hides it
	// from admin settings, while default true enables auth changes before RP config.
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
	// Mutation killed: accepting plain text origins makes origin allow-list
	// parsing ambiguous and can accidentally authorize request-controlled input.
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
