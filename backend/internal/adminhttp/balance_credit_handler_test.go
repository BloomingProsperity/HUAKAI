package adminhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

func TestAdminBalanceCreditHandlerRejectsTenantOperatorBeforeMoneyService(t *testing.T) {
	service := &balanceCreditServiceStub{}
	rec := invokeAdminBalanceCredit(t, AdminBalanceCreditDeps{
		Auth:    apiKeyAuthStub{ident: tenantOperator(7)},
		Service: service,
	}, `{"tenant_id":7,"user_id":3,"amount":"200.00000000","reason":"manual recharge","idempotency_key":"tenant-operator-should-not-credit"}`)

	assertAdminAPIKeyStatus(t, rec, http.StatusForbidden)
	if service.called {
		t.Fatalf("tenant_operator reached money service: %+v", service.got)
	}
}

func TestAdminBalanceCreditHandlerPlatformAdminReturnsNetBalance(t *testing.T) {
	service := &balanceCreditServiceStub{result: payment.AdminBalanceAdjustmentResult{
		TenantID:        7,
		UserID:          3,
		NewBalance:      decimal.RequireFromString("200.00000000"),
		CurrencyCode:    "USD",
		RechargeOrderID: 44,
	}}
	rec := invokeAdminBalanceCredit(t, AdminBalanceCreditDeps{
		Auth:    apiKeyAuthStub{ident: platformAdmin()},
		Service: service,
	}, `{"tenant_id":7,"user_id":3,"amount":"200.00000000","reason":"manual recharge","idempotency_key":"admin-idem-200"}`)

	assertAdminAPIKeyStatus(t, rec, http.StatusOK)
	if !service.called {
		t.Fatal("platform_admin did not reach money service")
	}
	// ActorID 必须是纯数字 TokenID:payment 归属 sink 是 int64(parseAdminActorID 会 ParseInt),
	// 传 AuditActor() 的 "admin_token:11" 会被解析成 0、丢充值归属。变异:改回 ident.AuditActor() → RED。
	if service.got.ActorID != "11" || service.got.TenantID != 7 || service.got.UserID != 3 ||
		!service.got.Amount.Equal(decimal.RequireFromString("200.00000000")) ||
		service.got.ExternalTradeNo != "admin-idem-200" {
		t.Fatalf("service input mismatch: %+v", service.got)
	}
	var body balanceCreditResponseBody
	decodeAdminAPIKeyBody(t, rec, &body)
	if body.NetBalance != "200.00000000" || body.CurrencyCode != "USD" || body.RechargeOrderID == nil || *body.RechargeOrderID != 44 {
		t.Fatalf("response mismatch: %+v", body)
	}
}

func TestAdminBalanceCreditHandlerRequiresIdempotencyKeyBeforeMoneyService(t *testing.T) {
	service := &balanceCreditServiceStub{}
	rec := invokeAdminBalanceCredit(t, AdminBalanceCreditDeps{
		Auth:    apiKeyAuthStub{ident: platformAdmin()},
		Service: service,
	}, `{"tenant_id":7,"user_id":3,"amount":"200.00000000","reason":"manual recharge"}`)

	assertAdminAPIKeyStatus(t, rec, http.StatusBadRequest)
	if service.called {
		t.Fatalf("missing idempotency key reached money service: %+v", service.got)
	}
}

func TestAdminBalanceCreditHandlerDebitGateErrorReturns400(t *testing.T) {
	service := &balanceCreditServiceStub{err: payment.ErrAdminDebitNotSupported}
	rec := invokeAdminBalanceCredit(t, AdminBalanceCreditDeps{
		Auth:    apiKeyAuthStub{ident: platformAdmin()},
		Service: service,
	}, `{"tenant_id":7,"user_id":3,"amount":"-10.00000000","reason":"manual debit blocked until durable debit event","idempotency_key":"admin-debit-blocked"}`)

	assertAdminAPIKeyStatus(t, rec, http.StatusBadRequest)
	assertBalanceCreditErrorCode(t, rec, "admin_debit_not_yet_supported")
	if !service.called {
		t.Fatal("debit gate must be decided by money service after idempotency lookup")
	}
}

func TestAdminBalanceCreditHandlerIdempotencyConflictReturns409(t *testing.T) {
	rec := invokeAdminBalanceCredit(t, AdminBalanceCreditDeps{
		Auth:    apiKeyAuthStub{ident: platformAdmin()},
		Service: &balanceCreditServiceStub{err: payment.ErrExternalTradeConflict},
	}, `{"tenant_id":7,"user_id":3,"amount":"200.00000000","reason":"manual recharge changed amount","idempotency_key":"admin-idem-conflict"}`)

	assertAdminAPIKeyStatus(t, rec, http.StatusConflict)
	assertBalanceCreditErrorCode(t, rec, "balance_adjustment_idempotency_conflict")
}

func TestAdminBalanceCreditHandlerDebitIdempotencyConflictReturns409(t *testing.T) {
	rec := invokeAdminBalanceCredit(t, AdminBalanceCreditDeps{
		Auth:    apiKeyAuthStub{ident: platformAdmin()},
		Service: &balanceCreditServiceStub{err: payment.ErrExternalTradeConflict},
	}, `{"tenant_id":7,"user_id":3,"amount":"-50.00000000","reason":"manual debit with reused key","idempotency_key":"admin-idem-conflict"}`)

	assertAdminAPIKeyStatus(t, rec, http.StatusConflict)
	assertBalanceCreditErrorCode(t, rec, "balance_adjustment_idempotency_conflict")
}

type balanceCreditServiceStub struct {
	result payment.AdminBalanceAdjustmentResult
	err    error
	called bool
	got    payment.AdminBalanceAdjustmentInput
}

func (s *balanceCreditServiceStub) AdminAdjustBalance(_ context.Context, input payment.AdminBalanceAdjustmentInput) (payment.AdminBalanceAdjustmentResult, error) {
	s.called = true
	s.got = input
	if s.err != nil {
		return payment.AdminBalanceAdjustmentResult{}, s.err
	}
	if s.result.CurrencyCode == "" {
		s.result = payment.AdminBalanceAdjustmentResult{
			TenantID:     input.TenantID,
			UserID:       input.UserID,
			NewBalance:   input.Amount,
			CurrencyCode: input.CurrencyCode,
		}
	}
	return s.result, nil
}

func invokeAdminBalanceCredit(t *testing.T, deps AdminBalanceCreditDeps, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/balances", func(r chi.Router) {
		MountBalanceCreditRoutes(r, deps)
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/balances/adjustments", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if strings.TrimSpace(rec.Body.String()) != "" && !json.Valid(rec.Body.Bytes()) {
		t.Fatalf("handler returned invalid JSON: %q", rec.Body.String())
	}
	return rec
}

func assertBalanceCreditErrorCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeAdminAPIKeyBody(t, rec, &body)
	if body.Error.Code != want {
		t.Fatalf("error code=%q want %q; body=%s", body.Error.Code, want, rec.Body.String())
	}
}

var _ adminAPIKeysAuth = apiKeyAuthStub{}
