package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestFinancialExportCSVRoutesMountedOnExportHandlers(t *testing.T) {
	r := buildTestRouter(t)
	for _, target := range []string{"/v1/admin/payments/export.csv", "/v1/admin/usage/export.csv"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, target+"?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z", nil)

		r.ServeHTTP(rec, req)

		if rec.Code == http.StatusNotFound {
			t.Fatalf("%s returned 404; CSV export route must be mounted", target)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "admin_backend_error") {
			t.Fatalf("%s body=%s want export handler admin backend error", target, body)
		}
		if strings.Contains(body, "payment admin dependency unset") {
			t.Fatalf("%s fell through to payment /{id} admin route: body=%s", target, body)
		}
	}
}
