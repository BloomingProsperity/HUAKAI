package paymenthttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

func TestCreateRechargeUsesSessionIdentityAndRejectsBodyTenantUser(t *testing.T) {
	service := &paymentServiceStub{}
	mux := mountPaymentRoutesWithSession(t, service, sessionauth.SessionIdentity{TenantID: 7, UserID: 42}, true)
	rec := postJSON(mux, "/v1/users/me/recharges", `{"amount":"50.00000000","currency":"USD","provider":"hmacpay","tenant_id":999,"user_id":666}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("body tenant/user spoof status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	if len(service.openCalls) != 0 {
		t.Fatalf("spoof body reached OpenRecharge: %+v", service.openCalls)
	}

	service.openResult = payment.OpenResult{Order: payment.Order{
		ID:              101,
		TenantID:        7,
		UserID:          42,
		ExternalTradeNo: externalTradeNoForTenant(7, "unit-create"),
		RechargeRef:     "rch_ref_101",
		Status:          payment.StatusPending,
		CreditedAmount:  decimal.RequireFromString("50.00000000"),
		CurrencyCode:    "USD",
		Provider:        "hmacpay",
		CreatedAt:       time.Date(2026, 6, 2, 8, 0, 0, 0, time.UTC),
	}}
	rec = postJSON(mux, "/v1/users/me/recharges", `{"amount":"50.00000000","currency":"USD","provider":"hmacpay","return_url":"https://app.example.test/return"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create recharge status=%d body=%s want 201", rec.Code, rec.Body.String())
	}
	if len(service.openCalls) != 1 {
		t.Fatalf("OpenRecharge calls=%d want 1", len(service.openCalls))
	}
	call := service.openCalls[0]
	if call.TenantID != 7 || call.UserID != 42 {
		t.Fatalf("OpenRecharge tenant/user=(%d,%d), want session (7,42)", call.TenantID, call.UserID)
	}
	if call.ExternalTradeNo == "" || !strings.HasPrefix(call.ExternalTradeNo, "rech_t7_") {
		t.Fatalf("external_trade_no=%q must be server-generated and tenant-prefixed", call.ExternalTradeNo)
	}
	if call.Provider != "hmacpay" || call.CurrencyCode != "USD" || !call.Amount.Equal(decimal.RequireFromString("50.00000000")) {
		t.Fatalf("OpenRecharge input mismatch: %+v", call)
	}
}

func TestWebhookBadSignatureRejectedBeforeMoneyService(t *testing.T) {
	now := time.Date(2026, 6, 2, 8, 30, 0, 0, time.UTC)
	service := &paymentServiceStub{}
	mux := mountPaymentRoutes(t, service, now)
	raw := []byte(`{"provider":"hmacpay","external_trade_no":"` + externalTradeNoForTenant(7, "bad-sig") + `","provider_event_id":"evt_bad","amount":"50.00000000","currency":"USD"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/payment/webhooks/hmacpay", strings.NewReader(string(raw)))
	req.Header = signedHeaders(now, raw, "wrong-secret")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad signature status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	if len(service.fulfillCalls) != 0 {
		t.Fatalf("bad signature reached money fulfillment: %+v", service.fulfillCalls)
	}
}

func TestWebhookVerifiedMismatchReturns200WithoutCompletion(t *testing.T) {
	now := time.Date(2026, 6, 2, 8, 40, 0, 0, time.UTC)
	service := &paymentServiceStub{
		fulfillResult: payment.CallbackResult{HTTPStatus: 200, AuditReason: payment.AuditReasonAmountMismatch},
		fulfillErr:    payment.ErrPaymentAmountMismatch,
	}
	mux := mountPaymentRoutes(t, service, now)
	tradeNo := externalTradeNoForTenant(7, "underpaid")
	raw := []byte(`{"provider":"hmacpay","external_trade_no":"` + tradeNo + `","provider_event_id":"evt_underpaid","amount":"5.00000000","currency":"USD"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/payment/webhooks/hmacpay", strings.NewReader(string(raw)))
	req.Header = signedHeaders(now, raw, "secret-one")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("verified mismatch status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if len(service.fulfillCalls) != 1 {
		t.Fatalf("FulfillVerifiedCallback calls=%d want 1", len(service.fulfillCalls))
	}
	cb := service.fulfillCalls[0]
	if cb.TenantID != 7 || cb.ExternalTradeNo != tradeNo || cb.ProviderEventID != "evt_underpaid" ||
		!cb.PaidAmount.Equal(decimal.RequireFromString("5.00000000")) {
		t.Fatalf("verified callback passed to service mismatch: %+v", cb)
	}
	var body webhookResponse
	decodeBody(t, rec, &body)
	if body.Completed || body.Idempotent || body.AuditReason != payment.AuditReasonAmountMismatch {
		t.Fatalf("mismatch response=%+v want audit-only no completion", body)
	}
}

func TestWebhookRouteProviderMustMatchVerifiedBodyProvider(t *testing.T) {
	now := time.Date(2026, 6, 2, 8, 45, 0, 0, time.UTC)
	service := &paymentServiceStub{}
	mux := mountPaymentRoutes(t, service, now)
	raw := []byte(`{"provider":"otherpay","external_trade_no":"` + externalTradeNoForTenant(7, "provider-cross") + `","provider_event_id":"evt_provider_cross","amount":"50.00000000","currency":"USD"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/payment/webhooks/hmacpay", strings.NewReader(string(raw)))
	req.Header = signedHeaders(now, raw, "secret-one")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("provider cross-sign status=%d body=%s want 200 audit-only", rec.Code, rec.Body.String())
	}
	if len(service.fulfillCalls) != 0 {
		t.Fatalf("cross-provider callback reached money fulfillment: %+v", service.fulfillCalls)
	}
	var body webhookResponse
	decodeBody(t, rec, &body)
	if body.AuditReason != payment.AuditReasonProviderMismatch || body.Completed || body.Idempotent {
		t.Fatalf("cross-provider response=%+v want provider mismatch audit-only", body)
	}
}

func TestWebhookReplayReturnsIdempotentNoDoubleCreditSignal(t *testing.T) {
	now := time.Date(2026, 6, 2, 8, 50, 0, 0, time.UTC)
	service := &paymentServiceStub{
		fulfillResult: payment.CallbackResult{
			HTTPStatus:  200,
			OrderID:     10,
			UserID:      42,
			Idempotent:  true,
			Completed:   true,
			AuditReason: payment.AuditReasonReplay,
		},
	}
	mux := mountPaymentRoutes(t, service, now)
	raw := []byte(`{"provider":"hmacpay","external_trade_no":"` + externalTradeNoForTenant(7, "replay") + `","provider_event_id":"evt_replay","amount":"50.00000000","currency":"USD"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/payment/webhooks/hmacpay", strings.NewReader(string(raw)))
	req.Header = signedHeaders(now, raw, "secret-one")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("replay status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	var body webhookResponse
	decodeBody(t, rec, &body)
	if !body.Idempotent || !body.Completed || body.AuditReason != payment.AuditReasonReplay {
		t.Fatalf("replay response=%+v want idempotent completed replay signal", body)
	}
}

func TestWebhookBackendErrorIsGeneric(t *testing.T) {
	now := time.Date(2026, 6, 2, 8, 55, 0, 0, time.UTC)
	service := &paymentServiceStub{
		fulfillErr: errors.New("postgres://internal-secret.example.test failed"),
	}
	mux := mountPaymentRoutes(t, service, now)
	raw := []byte(`{"provider":"hmacpay","external_trade_no":"` + externalTradeNoForTenant(7, "backend-error") + `","provider_event_id":"evt_backend_error","amount":"50.00000000","currency":"USD"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/payment/webhooks/hmacpay", strings.NewReader(string(raw)))
	req.Header = signedHeaders(now, raw, "secret-one")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("backend error status=%d body=%s want 503", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "postgres://") || strings.Contains(rec.Body.String(), "internal-secret") {
		t.Fatalf("public webhook leaked backend error: %s", rec.Body.String())
	}
}

func mountPaymentRoutesWithSession(t *testing.T, service PaymentService, ident sessionauth.SessionIdentity, passIdent bool) *chi.Mux {
	t.Helper()
	now := time.Date(2026, 6, 2, 8, 0, 0, 0, time.UTC)
	r := mountPaymentRoutes(t, service, now)
	if !passIdent {
		return r
	}
	withSession := chi.NewRouter()
	withSession.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(sessionauth.ContextWithSession(req.Context(), ident)))
		})
	})
	withSession.Mount("/", r)
	return withSession
}

func mountPaymentRoutes(t *testing.T, service PaymentService, now time.Time) *chi.Mux {
	t.Helper()
	r := chi.NewRouter()
	bindings := map[string]ProviderBinding{
		"hmacpay": {
			Provider: NewHMACProvider(WithClock(func() time.Time { return now })),
			Secret:   "secret-one",
		},
	}
	MountRoutes(r, Deps{
		Service:   service,
		Providers: bindings,
		Clock: func() time.Time {
			return now
		},
		ExternalTradeSuffix: func() (string, error) {
			return "unit-create", nil
		},
	})
	return r
}

func postJSON(handler http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(dst); err != nil {
		t.Fatalf("decode body=%s: %v", rec.Body.String(), err)
	}
}

type paymentServiceStub struct {
	openCalls     []payment.OpenInput
	openResult    payment.OpenResult
	openErr       error
	fulfillCalls  []payment.VerifiedCallback
	fulfillResult payment.CallbackResult
	fulfillErr    error
}

func (s *paymentServiceStub) OpenRecharge(_ context.Context, input payment.OpenInput) (payment.OpenResult, error) {
	s.openCalls = append(s.openCalls, input)
	if s.openErr != nil {
		return payment.OpenResult{}, s.openErr
	}
	return s.openResult, nil
}

func (s *paymentServiceStub) FulfillVerifiedCallback(_ context.Context, cb payment.VerifiedCallback) (payment.CallbackResult, error) {
	s.fulfillCalls = append(s.fulfillCalls, cb)
	if s.fulfillErr != nil {
		return s.fulfillResult, s.fulfillErr
	}
	if s.fulfillResult.HTTPStatus == 0 {
		s.fulfillResult.HTTPStatus = 200
	}
	return s.fulfillResult, nil
}

func TestWebhookMapsMissingTenantPrefixToAuditOnly200(t *testing.T) {
	now := time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC)
	service := &paymentServiceStub{}
	mux := mountPaymentRoutes(t, service, now)
	raw := []byte(`{"provider":"hmacpay","external_trade_no":"provider-native-id","provider_event_id":"evt_no_tenant","amount":"50.00000000","currency":"USD"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/payment/webhooks/hmacpay", strings.NewReader(string(raw)))
	req.Header = signedHeaders(now, raw, "secret-one")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("missing tenant prefix status=%d body=%s want 200 so provider does not retry an unmappable local order", rec.Code, rec.Body.String())
	}
	if len(service.fulfillCalls) != 0 {
		t.Fatalf("unmappable external_trade_no reached fulfillment: %+v", service.fulfillCalls)
	}
	if !strings.Contains(rec.Body.String(), payment.AuditReasonOrderNotFound) {
		t.Fatalf("body=%s must expose audit reason %s", rec.Body.String(), payment.AuditReasonOrderNotFound)
	}
}

var _ PaymentService = (*paymentServiceStub)(nil)
