package paymenthttp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

func TestHMACProviderRejectsForgedSignature(t *testing.T) {
	now := time.Date(2026, 6, 2, 8, 0, 0, 0, time.UTC)
	provider := NewHMACProvider(WithClock(func() time.Time { return now }))
	raw := []byte(`{"provider":"hmacpay","external_trade_no":"` + externalTradeNoForTenant(7, "forged") + `","provider_event_id":"evt_forged","amount":"50.00000000","currency":"USD"}`)
	headers := signedHeaders(now, raw, "wrong-secret")

	_, err := provider.VerifyWebhook(raw, headers, "right-secret")
	if err == nil {
		t.Fatal("forged HMAC signature was accepted; skipping verification would let a fake callback credit money")
	}
	if got := len(headers.Get(HeaderWebhookSignature)); got == 0 {
		t.Fatalf("test fixture did not send a signature header")
	}
}

func TestHMACProviderRejectsReplayOutsideTimestampWindow(t *testing.T) {
	now := time.Date(2026, 6, 2, 8, 0, 0, 0, time.UTC)
	old := now.Add(-10 * time.Minute)
	provider := NewHMACProvider(WithClock(func() time.Time { return now }), WithTimestampWindow(5*time.Minute))
	raw := []byte(`{"provider":"hmacpay","external_trade_no":"` + externalTradeNoForTenant(7, "stale") + `","provider_event_id":"evt_stale","amount":"50.00000000","currency":"USD"}`)
	headers := signedHeaders(old, raw, "secret-one")

	_, err := provider.VerifyWebhook(raw, headers, "secret-one")
	if err == nil {
		t.Fatal("stale signed webhook was accepted; timestamp replay window guard is missing")
	}
}

func TestHMACProviderParsesVerifiedCallbackWithoutTrustingTenantBody(t *testing.T) {
	now := time.Date(2026, 6, 2, 8, 0, 0, 0, time.UTC)
	tradeNo := externalTradeNoForTenant(7, "tenant-body-spoof")
	raw := []byte(`{"provider":"hmacpay","tenant_id":999,"external_trade_no":"` + tradeNo + `","provider_event_id":"evt_ok","amount":"50.00000000","currency":"usd"}`)
	provider := NewHMACProvider(WithClock(func() time.Time { return now }))

	cb, err := provider.VerifyWebhook(raw, signedHeaders(now, raw, "secret-two"), "secret-two")
	if err != nil {
		t.Fatalf("VerifyWebhook valid HMAC: %v", err)
	}
	if cb.TenantID != 0 {
		t.Fatalf("provider parsed tenant_id from body: got %d want 0; tenant must be derived by HUAKAI external_trade_no", cb.TenantID)
	}
	if cb.Provider != "hmacpay" || cb.ExternalTradeNo != tradeNo || cb.ProviderEventID != "evt_ok" ||
		cb.CurrencyCode != "USD" || !cb.PaidAmount.Equal(decimal.RequireFromString("50.00000000")) {
		t.Fatalf("verified callback mismatch: %+v", cb)
	}
}

func TestProviderRegistryRejectsMockInProduction(t *testing.T) {
	_, err := BuildProviderBindings(ProviderRegistryConfig{
		ReleaseMode: "production",
		EnableMock:  true,
	})
	if err == nil {
		t.Fatal("production registry accepted mock provider; mock verification must never be exposed in release mode")
	}

	bindings, err := BuildProviderBindings(ProviderRegistryConfig{
		ReleaseMode: "dev",
		EnableMock:  true,
	})
	if err != nil {
		t.Fatalf("dev mock registry: %v", err)
	}
	if _, ok := bindings["mock"]; !ok {
		t.Fatal("dev mock provider missing; fixture must prove only production rejects it")
	}
}

func signedHeaders(ts time.Time, raw []byte, secret string) http.Header {
	headers := http.Header{}
	unix := strconv.FormatInt(ts.UTC().Unix(), 10)
	headers.Set(HeaderWebhookTimestamp, unix)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(unix))
	mac.Write([]byte("."))
	mac.Write(raw)
	headers.Set(HeaderWebhookSignature, hex.EncodeToString(mac.Sum(nil)))
	return headers
}

var _ PaymentProvider = (*HMACProvider)(nil)
var _ = payment.VerifiedCallback{}
