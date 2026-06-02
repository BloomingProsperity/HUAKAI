package main

import "testing"

func TestPaymentProviderBindingsRejectMockInProductionReleaseMode(t *testing.T) {
	t.Setenv("HUAKAI_RELEASE_MODE", "production")

	_, err := buildPaymentProviderBindings(&Config{PaymentEnableMock: true})
	if err == nil {
		t.Fatal("production gateway wiring accepted mock payment provider")
	}
}

func TestPaymentProviderBindingsIncludesConfiguredHMACProviders(t *testing.T) {
	t.Setenv("HUAKAI_RELEASE_MODE", "dev")

	bindings, err := buildPaymentProviderBindings(&Config{PaymentHMACSecrets: map[string]string{
		"hmacpay": "secret-one",
	}})
	if err != nil {
		t.Fatalf("buildPaymentProviderBindings: %v", err)
	}
	if binding, ok := bindings["hmacpay"]; !ok || binding.Provider == nil || binding.Secret != "secret-one" {
		t.Fatalf("hmacpay binding=%+v ok=%v, want configured provider with secret", binding, ok)
	}
}
