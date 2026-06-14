package platformsettings

import (
	"errors"
	"testing"
)

// TestSiteBrandingKeysRegisteredWithEmptyDefault: the four extended-branding
// keys are allow-listed and default empty, so an unconfigured deployment shows
// no subtitle/contact/doc/api-base string.
// Regression caught: if a key were left out of orderedSettingKeys /
// defaultSettingValueMap, DefaultValue/IsAllowedKey go false here.
func TestSiteBrandingKeysRegisteredWithEmptyDefault(t *testing.T) {
	for _, key := range []SettingKey{KeySiteSubtitle, KeySiteContactInfo, KeySiteDocURL, KeySiteAPIBaseURL} {
		v, ok := DefaultValue(key)
		if !ok {
			t.Fatalf("%s must be a registered key", key)
		}
		if v != "" {
			t.Fatalf("%s default must be empty, got %q", key, v)
		}
		if !IsAllowedKey(key) {
			t.Fatalf("%s must be allowed", key)
		}
	}
}

// TestSiteBrandingTextValidation: subtitle/contact are OPTIONAL public text.
// Empty must be accepted (proves the optional routing) and plain text passes,
// but a control character is rejected (proves the text validator runs).
// Regression caught: drop KeySiteSubtitle/KeySiteContactInfo from the
// optional-public-text branch and Validate(key,"") hits the value=="" guard and
// is REJECTED -> the empty-accept assertion goes red; route them to a no-op and
// the control-char case is accepted -> that assertion goes red.
func TestSiteBrandingTextValidation(t *testing.T) {
	for _, key := range []SettingKey{KeySiteSubtitle, KeySiteContactInfo} {
		if v, err := ValidateValue(key, ""); err != nil || v != "" {
			t.Fatalf("%s empty must be accepted, got %q err=%v", key, v, err)
		}
		if v, err := ValidateValue(key, "Fast, clean gateway"); err != nil || v != "Fast, clean gateway" {
			t.Fatalf("%s plain text rejected: %q err=%v", key, v, err)
		}
		if _, err := ValidateValue(key, "line\nbreak"); !errors.Is(err, ErrInvalidValue) {
			t.Fatalf("%s must reject control characters, got err=%v", key, err)
		}
	}
}

// TestSiteBrandingURLValidation: doc/api-base are OPTIONAL http(s) URLs. Empty
// accepted, http(s) accepted, but a non-URL / non-http scheme is rejected.
// Regression caught: route these to optional-text instead of the URL validator
// and "not-a-url" / "javascript:alert(1)" would be ACCEPTED -> red here. The
// discriminating fixture: a bare token and a non-http scheme both pass a text
// validator but must fail the URL validator.
func TestSiteBrandingURLValidation(t *testing.T) {
	for _, key := range []SettingKey{KeySiteDocURL, KeySiteAPIBaseURL} {
		if v, err := ValidateValue(key, ""); err != nil || v != "" {
			t.Fatalf("%s empty must be accepted, got %q err=%v", key, v, err)
		}
		if v, err := ValidateValue(key, "https://docs.huakai.example/v1"); err != nil || v != "https://docs.huakai.example/v1" {
			t.Fatalf("%s https URL rejected: %q err=%v", key, v, err)
		}
		for _, bad := range []string{
			"not-a-url",               // no scheme/host
			"javascript:alert(1)",     // non-http scheme (XSS vector if surfaced)
			"ftp://files.example.com", // non-http scheme
			"//host/path",             // scheme-relative, no scheme
		} {
			if _, err := ValidateValue(key, bad); !errors.Is(err, ErrInvalidValue) {
				t.Fatalf("%s must reject %q as ErrInvalidValue, got err=%v", key, bad, err)
			}
		}
	}
}
