package adminhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/balanceledger"
)

func TestAdminBalanceCreditHandlerPassesTenantOperatorScope(t *testing.T) {
	service := &balanceCreditServiceStub{result: balanceledger.AdminBalanceAdjustmentResult{
		TransactionID: 44, TenantID: 7, UserID: 3, TargetKind: balanceledger.BalanceTargetUser,
		NewBalance: decimal.RequireFromString("20.00000000"), CurrencyCode: "USD",
	}}
	rec := invokeAdminBalanceCredit(t, AdminBalanceCreditDeps{
		Auth: apiKeyAuthStub{ident: tenantOperator(7)}, Service: service,
	}, `{"tenant_id":7,"user_id":3,"amount":"20.00000000","reason":"租户分发","idempotency_key":"tenant-credit-20"}`)

	assertAdminAPIKeyStatus(t, rec, http.StatusOK)
	if !service.called || service.got.ActorRole != "tenant_operator" || service.got.ActorScopeTenantID != 7 ||
		service.got.ActorRef != "admin_token:12" || service.got.IdempotencyKey != "tenant-credit-20" {
		t.Fatalf("租户管理员身份没有完整传入资金服务：%+v", service.got)
	}
	var body balanceCreditResponseBody
	decodeAdminAPIKeyBody(t, rec, &body)
	if body.TransactionID != 44 || body.TargetKind != balanceledger.BalanceTargetUser || body.UserID == nil || *body.UserID != 3 || body.NetBalance != "20.00000000" {
		t.Fatalf("用户余额响应错误：%+v", body)
	}
}

func TestAdminBalanceCreditHandlerReturnsTenantWalletWithoutFakeUser(t *testing.T) {
	service := &balanceCreditServiceStub{result: balanceledger.AdminBalanceAdjustmentResult{
		TransactionID: 51, TenantID: 7, TargetKind: balanceledger.BalanceTargetTenant,
		NewBalance: decimal.RequireFromString("200.00000000"), CurrencyCode: "USD", Idempotent: true,
	}}
	rec := invokeAdminBalanceCredit(t, AdminBalanceCreditDeps{
		Auth: apiKeyAuthStub{ident: platformAdmin()}, Service: service,
	}, `{"tenant_id":7,"amount":"200.00000000","reason":"平台下发","idempotency_key":"platform-tenant-200"}`)

	assertAdminAPIKeyStatus(t, rec, http.StatusOK)
	if service.got.UserID != 0 || service.got.ActorRole != "platform_admin" || service.got.ActorScopeTenantID != 0 {
		t.Fatalf("平台到租户的钱包形状错误：%+v", service.got)
	}
	var body balanceCreditResponseBody
	decodeAdminAPIKeyBody(t, rec, &body)
	if body.TargetKind != balanceledger.BalanceTargetTenant || body.UserID != nil || !body.Idempotent {
		t.Fatalf("租户钱包响应不应伪造 user_id：%+v", body)
	}
}

func TestAdminBalanceCreditHandlerRejectsOrdinaryRoleBeforeMoneyService(t *testing.T) {
	service := &balanceCreditServiceStub{}
	rec := invokeAdminBalanceCredit(t, AdminBalanceCreditDeps{
		Auth: apiKeyAuthStub{}, Service: service,
	}, `{"tenant_id":7,"amount":"10","reason":"非法","idempotency_key":"deny"}`)
	assertAdminAPIKeyStatus(t, rec, http.StatusForbidden)
	if service.called {
		t.Fatal("非管理身份不应到达资金服务")
	}
}

func TestAdminBalanceCreditHandlerRequiresIdempotencyKey(t *testing.T) {
	service := &balanceCreditServiceStub{}
	rec := invokeAdminBalanceCredit(t, AdminBalanceCreditDeps{
		Auth: apiKeyAuthStub{ident: platformAdmin()}, Service: service,
	}, `{"tenant_id":7,"amount":"10","reason":"缺幂等键"}`)
	assertAdminAPIKeyStatus(t, rec, http.StatusBadRequest)
	if service.called {
		t.Fatal("缺少幂等键不应到达资金服务")
	}
}

func TestTenantWalletReadIsTenantScoped(t *testing.T) {
	service := &balanceCreditServiceStub{wallet: balanceledger.TenantWalletSnapshot{
		TenantID: 7, Balance: decimal.NewFromInt(80), CurrencyCode: "USD", UpdatedAt: time.Date(2026, 7, 21, 4, 0, 0, 0, time.UTC),
	}}
	allowed := invokeAdminBalanceRequest(t, AdminBalanceCreditDeps{
		Auth: apiKeyAuthStub{ident: tenantOperator(7)}, Service: service,
	}, http.MethodGet, "/admin/v1/balances/tenant-wallet?tenant_id=7", "")
	assertAdminAPIKeyStatus(t, allowed, http.StatusOK)
	if service.walletTenantID != 7 {
		t.Fatalf("钱包查询 tenant_id=%d，want 7", service.walletTenantID)
	}

	service.walletTenantID = 0
	denied := invokeAdminBalanceRequest(t, AdminBalanceCreditDeps{
		Auth: apiKeyAuthStub{ident: tenantOperator(7)}, Service: service,
	}, http.MethodGet, "/admin/v1/balances/tenant-wallet?tenant_id=8", "")
	assertAdminAPIKeyStatus(t, denied, http.StatusForbidden)
	if service.walletTenantID != 0 {
		t.Fatal("跨租户钱包查询不应到达服务")
	}
}

func TestAdminBalanceCreditHandlerMapsMoneyErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"越权", balanceledger.ErrBalanceAdjustmentForbidden, http.StatusForbidden, "balance_adjustment_forbidden"},
		{"余额不足", balanceledger.ErrBalanceInsufficient, http.StatusConflict, "balance_insufficient"},
		{"幂等冲突", balanceledger.ErrExternalTradeConflict, http.StatusConflict, "balance_adjustment_idempotency_conflict"},
		{"租户不存在", balanceledger.ErrTenantNotFound, http.StatusNotFound, "tenant_not_found"},
		{"用户不存在", balanceledger.ErrUserNotFound, http.StatusNotFound, "user_not_found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rec := invokeAdminBalanceCredit(t, AdminBalanceCreditDeps{
				Auth: apiKeyAuthStub{ident: platformAdmin()}, Service: &balanceCreditServiceStub{err: test.err},
			}, `{"tenant_id":7,"amount":"10","reason":"测试","idempotency_key":"idem"}`)
			assertAdminAPIKeyStatus(t, rec, test.status)
			assertBalanceCreditErrorCode(t, rec, test.code)
		})
	}
}

type balanceCreditServiceStub struct {
	result         balanceledger.AdminBalanceAdjustmentResult
	err            error
	called         bool
	got            balanceledger.AdminBalanceAdjustmentInput
	wallet         balanceledger.TenantWalletSnapshot
	walletTenantID int64
	transactions   []balanceledger.BalanceTransaction
	listInput      balanceledger.ListTransactionsInput
}

func (s *balanceCreditServiceStub) GetTenantWallet(_ context.Context, tenantID int64) (balanceledger.TenantWalletSnapshot, error) {
	s.walletTenantID = tenantID
	if s.err != nil {
		return balanceledger.TenantWalletSnapshot{}, s.err
	}
	return s.wallet, nil
}

func (s *balanceCreditServiceStub) ListBalanceTransactions(_ context.Context, input balanceledger.ListTransactionsInput) ([]balanceledger.BalanceTransaction, error) {
	s.listInput = input
	if s.err != nil {
		return nil, s.err
	}
	return s.transactions, nil
}

func (s *balanceCreditServiceStub) AdminAdjustBalance(_ context.Context, input balanceledger.AdminBalanceAdjustmentInput) (balanceledger.AdminBalanceAdjustmentResult, error) {
	s.called = true
	s.got = input
	if s.err != nil {
		return balanceledger.AdminBalanceAdjustmentResult{}, s.err
	}
	return s.result, nil
}

func invokeAdminBalanceCredit(t *testing.T, deps AdminBalanceCreditDeps, body string) *httptest.ResponseRecorder {
	t.Helper()
	return invokeAdminBalanceRequest(t, deps, http.MethodPost, "/admin/v1/balances/adjustments", body)
}

func invokeAdminBalanceRequest(t *testing.T, deps AdminBalanceCreditDeps, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/balances", func(r chi.Router) { MountBalanceCreditRoutes(r, deps) })
	req := httptest.NewRequest(method, target, bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if strings.TrimSpace(rec.Body.String()) != "" && !json.Valid(rec.Body.Bytes()) {
		t.Fatalf("handler 返回了无效 JSON：%q", rec.Body.String())
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
		t.Fatalf("error code=%q，want %q；body=%s", body.Error.Code, want, rec.Body.String())
	}
}
