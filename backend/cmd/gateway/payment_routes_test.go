package main

import (
	"context"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

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

func TestPaymentServiceOptionsRegistersMockProviderWhenEnabled(t *testing.T) {
	svc := payment.NewService(payment.NewMemoryStore(), paymentServiceOptions(&Config{PaymentEnableMock: true})...)

	res, err := svc.CreateOrder(context.Background(), payment.CreateOrderInput{
		TenantID: 1, UserID: 2, AmountCents: 100, OutTradeNo: "mock-enabled", ProviderKind: payment.ProviderTest,
	})
	if err != nil {
		t.Fatalf("CreateOrder with mock/test provider: %v", err)
	}
	if res.Order.ProviderKind != payment.ProviderTest {
		t.Fatalf("provider=%q want test", res.Order.ProviderKind)
	}
}
