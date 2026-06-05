package platformsettings

import (
	"errors"
	"strings"
	"testing"
)

func TestPaymentProviderConfigSettingIsNonSecretJSONObject(t *testing.T) {
	value := `{"manual":{"enabled":true,"checkout_url":""},"taobao":{"enabled":true,"checkout_url":"https://pay.example/taobao"}}`
	got, err := ValidateValue(KeyPaymentProviderConfig, value)
	if err != nil {
		t.Fatalf("ValidateValue payment_provider_config: %v", err)
	}
	if got != value {
		t.Fatalf("normalized=%q want original JSON object", got)
	}
	def, ok := DefaultValue(KeyPaymentProviderConfig)
	if !ok || !strings.Contains(def, `"manual"`) || !strings.Contains(def, `"taobao"`) {
		t.Fatalf("default=%q ok=%v want manual+taobao JSON object", def, ok)
	}
}

func TestPaymentProviderConfigSettingRejectsIncompleteProviderConfig(t *testing.T) {
	cases := []string{
		`{}`,
		`{"manual":{"enabled":true,"checkout_url":""}}`,
		`{"manual":{"enabled":true,"checkout_url":"https://x"},"taobao":{"enabled":false,"checkout_url":""}}`,
		`{"manual":{"enabled":true,"checkout_url":""},"taobao":{"enabled":true,"checkout_url":""}}`,
	}
	for _, value := range cases {
		if _, err := ValidateValue(KeyPaymentProviderConfig, value); !errors.Is(err, ErrInvalidValue) {
			t.Fatalf("ValidateValue(%s) err=%v want ErrInvalidValue", value, err)
		}
	}
}
