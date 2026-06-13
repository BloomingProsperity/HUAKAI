package platformsettings

import (
	"errors"
	"testing"
)

// TestAdminNotificationEmailValidation: the admin_notification_email setting
// accepts empty (the safe default that keeps the daily inspection off) and a
// single plausible address, and rejects whitespace/multi-address/malformed input
// that could corrupt an SMTP header.
// Regression caught: if the email validator were dropped (fell through to plain
// public-text), a value like "a@b, c@d" or "no-at-sign" would be accepted and
// later poison the recipient header.
func TestAdminNotificationEmailValidation(t *testing.T) {
	if v, err := ValidateValue(KeyAdminNotificationEmail, ""); err != nil || v != "" {
		t.Fatalf("empty must be accepted (default off), got %q err=%v", v, err)
	}
	if v, err := ValidateValue(KeyAdminNotificationEmail, "ops@huakai.example"); err != nil || v != "ops@huakai.example" {
		t.Fatalf("valid address rejected: %q err=%v", v, err)
	}
	for _, bad := range []string{
		"no-at-sign",
		"a@b",                 // host without a dot
		"a@b.com, c@d.com",    // multi-address list
		"a@b.com;c@d.com",     // semicolon list
		"with space@b.com",    // internal whitespace
		"trailing@",           // empty host
		"@leading.com",        // empty local-part
		"two@@at.com",         // two @
		"line@break.com\nX:1", // header injection attempt
	} {
		if _, err := ValidateValue(KeyAdminNotificationEmail, bad); !errors.Is(err, ErrInvalidValue) {
			t.Fatalf("expected %q to be rejected as ErrInvalidValue, got err=%v", bad, err)
		}
	}
}

// TestAdminNotificationEmailDefaultEmpty: the key is registered with an empty
// default so an unconfigured deployment resolves no recipient.
func TestAdminNotificationEmailDefaultEmpty(t *testing.T) {
	v, ok := DefaultValue(KeyAdminNotificationEmail)
	if !ok {
		t.Fatalf("admin_notification_email must be a registered key")
	}
	if v != "" {
		t.Fatalf("default must be empty, got %q", v)
	}
	if !IsAllowedKey(KeyAdminNotificationEmail) {
		t.Fatalf("key must be allowed")
	}
}
