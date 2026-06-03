package platformsettings

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

const (
	GlobalScope   = "global"
	SourceDefault = "default"
	SourceDB      = "db"
)

type SettingKey string

const (
	KeyRegistrationEnabled         SettingKey = "registration_enabled"
	KeyInvitationRequired          SettingKey = "invitation_required"
	KeyCaptchaEnabled              SettingKey = "captcha_enabled"
	KeyCaptchaProvider             SettingKey = "captcha_provider"
	KeyCaptchaSiteKey              SettingKey = "captcha_site_key"
	KeyOAuthProvidersEnabled       SettingKey = "oauth_providers_enabled"
	KeyPromoEnabled                SettingKey = "promo_enabled"
	KeyStreamTimeoutSeconds        SettingKey = "stream_timeout_seconds"
	KeyCooldown429Seconds          SettingKey = "cooldown_429_seconds"
	KeyCooldown529Seconds          SettingKey = "cooldown_529_seconds"
	KeyResponseHeaderDenyExtra     SettingKey = "response_header_deny_extra"
	KeyResponseHeaderAllowOverride SettingKey = "response_header_allow_override"
)

var (
	ErrUnknownKey          = errors.New("platformsettings: unknown setting key")
	ErrInvalidValue        = errors.New("platformsettings: invalid setting value")
	ErrStoreNotConfigured  = errors.New("platformsettings: store not configured")
	orderedSettingKeys     = []SettingKey{KeyRegistrationEnabled, KeyInvitationRequired, KeyCaptchaEnabled, KeyCaptchaProvider, KeyCaptchaSiteKey, KeyOAuthProvidersEnabled, KeyPromoEnabled, KeyStreamTimeoutSeconds, KeyCooldown429Seconds, KeyCooldown529Seconds, KeyResponseHeaderDenyExtra, KeyResponseHeaderAllowOverride}
	defaultSettingValueMap = map[SettingKey]string{
		KeyRegistrationEnabled:         "false",
		KeyInvitationRequired:          "true",
		KeyCaptchaEnabled:              "false",
		KeyCaptchaProvider:             "",
		KeyCaptchaSiteKey:              "",
		KeyOAuthProvidersEnabled:       "",
		KeyPromoEnabled:                "false",
		KeyStreamTimeoutSeconds:        "120",
		KeyCooldown429Seconds:          "60",
		KeyCooldown529Seconds:          "300",
		KeyResponseHeaderDenyExtra:     "",
		KeyResponseHeaderAllowOverride: "",
	}
)

func AllKeys() []SettingKey {
	return append([]SettingKey(nil), orderedSettingKeys...)
}

func DefaultValue(key SettingKey) (string, bool) {
	value, ok := defaultSettingValueMap[key]
	return value, ok
}

func IsAllowedKey(key SettingKey) bool {
	_, ok := defaultSettingValueMap[key]
	return ok
}

func ParseKey(raw string) (SettingKey, error) {
	key := SettingKey(strings.TrimSpace(raw))
	if !IsAllowedKey(key) {
		return "", fmt.Errorf("%w: %s", ErrUnknownKey, raw)
	}
	return key, nil
}

func ValidateValue(key SettingKey, raw string) (string, error) {
	if !IsAllowedKey(key) {
		return "", fmt.Errorf("%w: %s", ErrUnknownKey, key)
	}
	value := strings.TrimSpace(raw)
	if key == KeyResponseHeaderDenyExtra || key == KeyResponseHeaderAllowOverride {
		return validateHeaderListValue(key, value)
	}
	if value == "" {
		return "", fmt.Errorf("%w: %s", ErrInvalidValue, key)
	}
	switch key {
	case KeyRegistrationEnabled, KeyInvitationRequired, KeyCaptchaEnabled, KeyPromoEnabled:
		return validateBoolValue(key, value)
	case KeyStreamTimeoutSeconds, KeyCooldown429Seconds, KeyCooldown529Seconds:
		return validatePositiveIntValue(key, value)
	case KeyCaptchaProvider:
		return validateCaptchaProvider(value)
	default:
		return validatePublicTextValue(key, value)
	}
}

func validateBoolValue(key SettingKey, value string) (string, error) {
	switch value {
	case "true", "false":
		return value, nil
	default:
		return "", fmt.Errorf("%w: %s must be true or false", ErrInvalidValue, key)
	}
}

func validatePositiveIntValue(key SettingKey, value string) (string, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return "", fmt.Errorf("%w: %s must be positive integer seconds", ErrInvalidValue, key)
	}
	return strconv.Itoa(parsed), nil
}

func validateCaptchaProvider(value string) (string, error) {
	switch value {
	case "turnstile":
		return value, nil
	default:
		return "", fmt.Errorf("%w: captcha_provider", ErrInvalidValue)
	}
}

func validatePublicTextValue(key SettingKey, value string) (string, error) {
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%w: %s contains control character", ErrInvalidValue, key)
		}
	}
	return value, nil
}

func validateHeaderListValue(key SettingKey, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	parts := strings.Split(value, ",")
	if len(parts) > 20 {
		return "", fmt.Errorf("%w: %s allows at most 20 header patterns", ErrInvalidValue, key)
	}
	for _, part := range parts {
		if part == "" {
			return "", fmt.Errorf("%w: %s contains empty header pattern", ErrInvalidValue, key)
		}
		if len(part) > 64 {
			return "", fmt.Errorf("%w: %s header pattern too long", ErrInvalidValue, key)
		}
		if !validHeaderPattern(part) {
			return "", fmt.Errorf("%w: %s contains invalid header pattern", ErrInvalidValue, key)
		}
	}
	return strings.Join(parts, ","), nil
}

func validHeaderPattern(value string) bool {
	for _, r := range value {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}
