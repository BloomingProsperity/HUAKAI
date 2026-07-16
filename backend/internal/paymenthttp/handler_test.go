// HUAKAI · iKun

package paymenthttp

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

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

func sampleOrder() payment.Order {
	ts := time.Date(2026, 5, 29, 1, 2, 3, 0, time.UTC)
	return payment.Order{
		ID: 1, TenantID: 5, UserID: 7, OutTradeNo: "t-1", AmountCents: 1000, CurrencyCode: "USD",
		Status: payment.StatusCompleted, ProviderKind: payment.ProviderManual,
		CreatedByAdminID: 99, ConfirmedByAdminID: 99, ConfirmReason: "manual ok",
		ProviderOrderRef: "ref-x", RequestFingerprint: "fp-secret-abc",
		CreatedAt: ts, UpdatedAt: ts,
	}
}

// 守数据泄露: 用户视图绝不暴露任何内部/管理字段, 且全 snake_case。
// mutation: handler 改回直返 payment.Order → 序列化出 PascalCase + 内部字段 + 指纹值 → 红。
func TestUserOrderViewHidesInternalFields(t *testing.T) {
	raw, err := json.Marshal(toOrderView(sampleOrder()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(raw)
	for _, leaked := range []string{
		"created_by_admin_id", "confirmed_by_admin_id", "confirm_reason",
		"provider_order_ref", "request_fingerprint",
		"RequestFingerprint", "CreatedByAdminID", "ConfirmReason", "OutTradeNo",
	} {
		if strings.Contains(js, leaked) {
			t.Fatalf("user order view leaked field %q: %s", leaked, js)
		}
	}
	// 内部指纹值本身绝不出现。
	if strings.Contains(js, "fp-secret-abc") {
		t.Fatalf("user order view leaked request fingerprint value: %s", js)
	}
	// 公开字段必须在且是 snake_case。
	if !strings.Contains(js, `"out_trade_no"`) || !strings.Contains(js, `"amount_cents"`) {
		t.Fatalf("user order view missing public snake_case fields: %s", js)
	}
}

func TestAdminOrderViewIncludesAdminFieldsSnakeCase(t *testing.T) {
	raw, err := json.Marshal(toAdminOrderView(sampleOrder()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(raw)
	if !strings.Contains(js, `"confirmed_by_admin_id"`) || !strings.Contains(js, `"confirm_reason"`) {
		t.Fatalf("admin order view missing admin fields: %s", js)
	}
	// admin 视图仍不暴露纯内部 request_fingerprint。
	if strings.Contains(js, "fp-secret-abc") || strings.Contains(js, "request_fingerprint") {
		t.Fatalf("admin order view exposed request fingerprint: %s", js)
	}
	// 不得出现 PascalCase 字段名。
	if strings.Contains(js, "OutTradeNo") || strings.Contains(js, "AmountCents") {
		t.Fatalf("admin order view leaked PascalCase field: %s", js)
	}
}

// ---- 5c: order_kind / subscription 透传 + 渲染 ----

// 守渲染: 充值单 order_kind=topup 且无 subscription_plan_id; 订阅单 order_kind=subscription 且带 plan_id。
// mutation: toOrderView 漏拷 OrderKind → 订阅单 order_kind 空 → 红; 漏拷 SubscriptionPlanID → plan_id 不出现 → 红。
func TestOrderViewRendersKind(t *testing.T) {
	topup := sampleOrder()
	topup.OrderKind = payment.OrderKindTopup
	rawTopup, _ := json.Marshal(toOrderView(topup))
	if !strings.Contains(string(rawTopup), `"order_kind":"topup"`) {
		t.Fatalf("topup view missing order_kind=topup: %s", rawTopup)
	}
	if strings.Contains(string(rawTopup), "subscription_plan_id") {
		t.Fatalf("topup view must omit subscription_plan_id (nil): %s", rawTopup)
	}

	planID := int64(42)
	sub := sampleOrder()
	sub.OrderKind = payment.OrderKindSubscription
	sub.SubscriptionPlanID = &planID
	rawSub, _ := json.Marshal(toOrderView(sub))
	if !strings.Contains(string(rawSub), `"order_kind":"subscription"`) {
		t.Fatalf("subscription view missing order_kind=subscription: %s", rawSub)
	}
	if !strings.Contains(string(rawSub), `"subscription_plan_id":42`) {
		t.Fatalf("subscription view missing subscription_plan_id=42: %s", rawSub)
	}
}

type captureService struct {
	gotCreate             payment.CreateOrderInput
	createRes             payment.CreateOrderResult
	createErr             error
	confirmRes            payment.FulfillResult
	confirmErr            error
	gotAdminList          payment.OrderListFilter
	adminListRes          []payment.Order
	adminListErr          error
	gotDashboard          payment.DashboardFilter
	dashboardRes          payment.DashboardStats
	dashboardErr          error
	gotAuditTenantID      int64
	gotAuditOrderID       int64
	auditEvents           []payment.AuditEvent
	auditErr              error
	gotRetry              payment.RetryFulfillmentInput
	retryRes              payment.FulfillResult
	retryErr              error
	gotProviderConfigKind payment.ProviderKind
	providerConfigRes     payment.ProviderRuntimeConfig
	providerConfigErr     error
	gotSetProviderConfig  payment.ProviderRuntimeConfigInput
	setProviderConfigRes  payment.ProviderRuntimeConfig
	setProviderConfigErr  error
	gotCancel             payment.CancelOrderInput
	cancelRes             payment.Order
	cancelErr             error
	gotRefund             payment.RefundOrderInput
	refundRes             payment.RefundResult
	refundErr             error
}

func (s *captureService) CreateOrder(_ context.Context, in payment.CreateOrderInput) (payment.CreateOrderResult, error) {
	s.gotCreate = in
	return s.createRes, s.createErr
}

func (s *captureService) AdminConfirmPaid(_ context.Context, _ payment.AdminConfirmPaidInput) (payment.FulfillResult, error) {
	return s.confirmRes, s.confirmErr
}

func (s *captureService) GetOrder(_ context.Context, _, _ int64) (payment.Order, error) {
	return payment.Order{}, nil
}
func (s *captureService) ListAuditEvents(_ context.Context, tenantID, orderID int64) ([]payment.AuditEvent, error) {
	s.gotAuditTenantID = tenantID
	s.gotAuditOrderID = orderID
	return s.auditEvents, s.auditErr
}
func (s *captureService) GetBalance(_ context.Context, _, _ int64) (payment.Balance, error) {
	return payment.Balance{}, nil
}
func (s *captureService) ListOrders(_ context.Context, _, _ int64, _ int) ([]payment.Order, error) {
	return nil, nil
}
func (s *captureService) AdminListOrders(_ context.Context, in payment.OrderListFilter) ([]payment.Order, error) {
	s.gotAdminList = in
	return s.adminListRes, s.adminListErr
}
func (s *captureService) DashboardStats(_ context.Context, in payment.DashboardFilter) (payment.DashboardStats, error) {
	s.gotDashboard = in
	return s.dashboardRes, s.dashboardErr
}
func (s *captureService) RetryFulfillment(_ context.Context, in payment.RetryFulfillmentInput) (payment.FulfillResult, error) {
	s.gotRetry = in
	return s.retryRes, s.retryErr
}
func (s *captureService) GetProviderRuntimeConfig(_ context.Context, kind payment.ProviderKind) (payment.ProviderRuntimeConfig, error) {
	s.gotProviderConfigKind = kind
	return s.providerConfigRes, s.providerConfigErr
}
func (s *captureService) SetProviderRuntimeConfig(_ context.Context, in payment.ProviderRuntimeConfigInput) (payment.ProviderRuntimeConfig, error) {
	s.gotSetProviderConfig = in
	return s.setProviderConfigRes, s.setProviderConfigErr
}

func (s *captureService) CancelOrder(_ context.Context, in payment.CancelOrderInput) (payment.Order, error) {
	s.gotCancel = in
	return s.cancelRes, s.cancelErr
}

func (s *captureService) RefundOrder(_ context.Context, in payment.RefundOrderInput) (payment.RefundResult, error) {
	s.gotRefund = in
	return s.refundRes, s.refundErr
}

type fakeAdminAuth struct{ ident admin.AdminIdentity }

func (a fakeAdminAuth) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return a.ident, nil
}

func newAdminTestRouter(svc Service) http.Handler {
	r := chi.NewRouter()
	d := AdminDeps{
		Auth:    fakeAdminAuth{ident: admin.AdminIdentity{Role: admin.RolePlatformAdmin, TokenID: 99}},
		Service: svc,
	}
	r.Route("/orders", func(r chi.Router) { MountPaymentAdminRoutes(r, d) })
	return r
}

// 守透传: 建单 handler 必须把 order_kind + subscription_plan_id 原样传给 service。
// mutation: handler 漏带这两个字段 → 捕获到的 OrderKind 为空 / plan 指针 nil → 红 (订阅单会被当充值建错)。
func TestCreateOrderPassesSubscriptionFields(t *testing.T) {
	planID := int64(42)
	svc := &captureService{createRes: payment.CreateOrderResult{Order: payment.Order{ID: 1, OrderKind: payment.OrderKindSubscription, SubscriptionPlanID: &planID}}}
	router := newAdminTestRouter(svc)

	body, _ := json.Marshal(createOrderRequest{
		TenantID: 5, UserID: 7, AmountCents: 1990, OutTradeNo: "sub-1",
		OrderKind: payment.OrderKindSubscription, SubscriptionPlanID: &planID,
	})
	req := httptest.NewRequest(http.MethodPost, "/orders/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if svc.gotCreate.OrderKind != payment.OrderKindSubscription {
		t.Fatalf("service got order_kind = %q, want subscription", svc.gotCreate.OrderKind)
	}
	if svc.gotCreate.SubscriptionPlanID == nil || *svc.gotCreate.SubscriptionPlanID != planID {
		t.Fatalf("service got subscription_plan_id = %v, want %d", svc.gotCreate.SubscriptionPlanID, planID)
	}
}

// 守响应分流: 订阅单 confirm 表 subscription 授予且不渲染 credit/balance (订阅零入账, 渲染零值入账会误导)。
// mutation: handler 总写 credit/balance (删订阅分支) → 出现 "credit" 键 → 红; 漏 subscription → "subscription" 缺 → 红。
func TestConfirmSubscriptionOrderSurfacesGrantNoCredit(t *testing.T) {
	planID := int64(42)
	exp := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	svc := &captureService{confirmRes: payment.FulfillResult{
		Order: payment.Order{ID: 1, OrderKind: payment.OrderKindSubscription, SubscriptionPlanID: &planID},
		Subscription: &payment.SubscriptionGrant{
			UserSubscriptionID: 9, PlanID: planID, ResultKind: "created", NewExpiresAt: exp, AppliedValidityDays: 30,
		},
	}}
	router := newAdminTestRouter(svc)

	body, _ := json.Marshal(confirmRequest{TenantID: 5, ConfirmReason: "manual"})
	req := httptest.NewRequest(http.MethodPost, "/orders/1/confirm", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	grantRaw, ok := resp["subscription"]
	if !ok {
		t.Fatalf("subscription order confirm missing subscription grant: %s", rec.Body.String())
	}
	var grant subscriptionGrantView
	if err := json.Unmarshal(grantRaw, &grant); err != nil {
		t.Fatalf("decode grant: %v", err)
	}
	if grant.PlanID != planID || grant.UserSubscriptionID != 9 || grant.ResultKind != "created" {
		t.Fatalf("grant mismatch: %+v", grant)
	}
	if _, leaked := resp["credit"]; leaked {
		t.Fatalf("subscription order confirm must not render credit: %s", rec.Body.String())
	}
	if _, leaked := resp["balance_cents"]; leaked {
		t.Fatalf("subscription order confirm must not render balance_cents: %s", rec.Body.String())
	}
}

// 守错误映射: 内存 store 确认订阅单回 ErrSubscriptionOrderRequiresPG → 503 + 专属 code。
// 判别陷阱: writePaymentError 的 default 分支也回 503, 故必须断言 code 字符串而非仅状态码;
// mutation: 删掉该 errors.Is 分支 → 落 default → code 变 payment_backend_error → 红。
func TestConfirmSubscriptionOrderRequiresPGMapsTo503(t *testing.T) {
	svc := &captureService{confirmErr: payment.ErrSubscriptionOrderRequiresPG}
	router := newAdminTestRouter(svc)

	body, _ := json.Marshal(confirmRequest{TenantID: 5})
	req := httptest.NewRequest(http.MethodPost, "/orders/1/confirm", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if resp.Error.Code != "subscription_order_requires_pg" {
		t.Fatalf("error code = %q, want subscription_order_requires_pg (default 503 would give payment_backend_error)", resp.Error.Code)
	}
}

// 守退款可用余额错误映射: 金额有效但可用余额不足应是专属 409, 不能落 default 503。
func TestRefundExceedsAvailableMapsTo409(t *testing.T) {
	rec := httptest.NewRecorder()

	writePaymentError(rec, payment.ErrRefundExceedsAvailable)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if resp.Error.Code != "refund_exceeds_available" {
		t.Fatalf("error code = %q, want refund_exceeds_available", resp.Error.Code)
	}
}

// 守充值单未回退: 充值单 confirm 仍渲染 credit + balance, 不渲染 subscription。
// mutation: handler 把分支反了 → 充值单丢 credit / 误加 subscription → 红。
func TestConfirmTopupOrderSurfacesCreditNoSubscription(t *testing.T) {
	svc := &captureService{confirmRes: payment.FulfillResult{
		Order:        payment.Order{ID: 1, OrderKind: payment.OrderKindTopup},
		Credit:       payment.CreditRecord{ID: 3, AmountCents: 1000, CurrencyCode: "USD"},
		BalanceCents: 1000,
	}}
	router := newAdminTestRouter(svc)

	body, _ := json.Marshal(confirmRequest{TenantID: 5})
	req := httptest.NewRequest(http.MethodPost, "/orders/1/confirm", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := resp["credit"]; !ok {
		t.Fatalf("topup confirm missing credit: %s", rec.Body.String())
	}
	if _, ok := resp["balance_cents"]; !ok {
		t.Fatalf("topup confirm missing balance_cents: %s", rec.Body.String())
	}
	if _, leaked := resp["subscription"]; leaked {
		t.Fatalf("topup confirm must not render subscription grant: %s", rec.Body.String())
	}
}

// 守 C1: admin cancel 路由把 {id}/tenant/actor=admin 正确传给 service, 返回 200 + cancelled 视图。
// Mutation: handler 不传 ActorKind 或漏 {id} -> 断言红。
func TestAdminCancelOrderRouteWiresService(t *testing.T) {
	svc := &captureService{cancelRes: payment.Order{ID: 7, Status: payment.StatusCancelled}}
	router := newAdminTestRouter(svc)
	body, _ := json.Marshal(cancelRequest{TenantID: 5, Reason: "ops cancel"})
	req := httptest.NewRequest(http.MethodPost, "/orders/7/cancel", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if svc.gotCancel.OrderID != 7 || svc.gotCancel.TenantID != 5 || svc.gotCancel.UserID != 0 || svc.gotCancel.ActorKind != payment.ActorKindAdmin {
		t.Fatalf("admin cancel did not wire input correctly: %+v", svc.gotCancel)
	}
}

// 守 C1 refund: admin refund 路由把 path id / tenant / amount / idempotency_key / actor / request_id 传给 service,
// 并返回 refund + balance。Mutation: 漏传 key 或 actor → 捕获断言红; 响应漏 balance → 红。
func TestAdminRefundOrderRouteWiresService(t *testing.T) {
	svc := &captureService{refundRes: payment.RefundResult{
		Order:        payment.Order{ID: 7, Status: payment.StatusRefunded},
		Refund:       payment.RefundRecord{ID: 3, AmountCents: 250, CurrencyCode: "USD", IdempotencyKey: "refund-http"},
		BalanceCents: 750,
	}}
	router := newAdminTestRouter(svc)
	body, _ := json.Marshal(refundRequest{TenantID: 5, AmountCents: 250, IdempotencyKey: "refund-http", Reason: "ops refund"})
	req := httptest.NewRequest(http.MethodPost, "/orders/7/refund", bytes.NewReader(body))
	req.Header.Set("X-Request-Id", "req-http-refund")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if svc.gotRefund.OrderID != 7 || svc.gotRefund.TenantID != 5 || svc.gotRefund.AmountCents != 250 ||
		svc.gotRefund.IdempotencyKey != "refund-http" || svc.gotRefund.ActorKind != payment.ActorKindAdmin ||
		svc.gotRefund.ActorID != 99 || svc.gotRefund.RequestID != "req-http-refund" {
		t.Fatalf("admin refund did not wire input correctly: %+v", svc.gotRefund)
	}
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := resp["refund"]; !ok {
		t.Fatalf("refund response missing refund object: %s", rec.Body.String())
	}
	if string(resp["balance_cents"]) != "750" {
		t.Fatalf("balance_cents=%s want 750; body=%s", resp["balance_cents"], rec.Body.String())
	}
}

// 守 C1 refund 错误映射: 非可退款状态是 409 专属 code, 不支持订单种类是 422 专属 code。
// Mutation: 两者落 default/invalid request 会返回不同 code 或状态, 本测试变红。
func TestRefundErrorsMapToDistinctCodes(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"not refundable", payment.ErrOrderNotRefundable, http.StatusConflict, "order_not_refundable"},
		{"unsupported kind", payment.ErrRefundUnsupportedKind, http.StatusUnprocessableEntity, "refund_unsupported_kind"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := &captureService{refundErr: c.err}
			router := newAdminTestRouter(svc)
			body, _ := json.Marshal(refundRequest{TenantID: 5, AmountCents: 250, IdempotencyKey: "refund-err"})
			req := httptest.NewRequest(http.MethodPost, "/orders/7/refund", bytes.NewReader(body))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != c.wantStatus {
				t.Fatalf("status=%d want %d; body=%s", rec.Code, c.wantStatus, rec.Body.String())
			}
			var resp struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if resp.Error.Code != c.wantCode {
				t.Fatalf("code=%q want %q; body=%s", resp.Error.Code, c.wantCode, rec.Body.String())
			}
		})
	}
}
