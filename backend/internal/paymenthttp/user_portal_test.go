// HUAKAI · iKun

package paymenthttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

// portalService 是用户门户测试用的可配置 Service stub:
// CreateOrder 捕获入参并回 createRes; GetOrder 回 getOrder/getErr (用于归属校验)。
// RefundOrder 被故意标红字段: 任何门户路径调用它都视为越权动钱 → 测试断言它从不被调用。
type portalService struct {
	gotCreate     payment.CreateOrderInput
	createRes     payment.CreateOrderResult
	createErr     error
	getOrder      payment.Order
	getErr        error
	refundCalled  bool
	refundOrderID int64
}

func (s *portalService) CreateOrder(_ context.Context, in payment.CreateOrderInput) (payment.CreateOrderResult, error) {
	s.gotCreate = in
	if s.createErr != nil {
		return payment.CreateOrderResult{}, s.createErr
	}
	if s.createRes.Order.ID == 0 {
		// 默认回一张与入参一致的 topup 单, 方便断言渲染。
		s.createRes.Order = payment.Order{
			ID: 1, TenantID: in.TenantID, UserID: in.UserID, OutTradeNo: in.OutTradeNo,
			AmountCents: in.AmountCents, CurrencyCode: "USD", Status: payment.StatusPending,
			ProviderKind: in.ProviderKind, OrderKind: in.OrderKind,
		}
	}
	return s.createRes, nil
}

func (s *portalService) AdminConfirmPaid(context.Context, payment.AdminConfirmPaidInput) (payment.FulfillResult, error) {
	return payment.FulfillResult{}, nil
}

func (s *portalService) GetOrder(_ context.Context, _, _ int64) (payment.Order, error) {
	return s.getOrder, s.getErr
}

func (s *portalService) ListAuditEvents(context.Context, int64, int64) ([]payment.AuditEvent, error) {
	return nil, nil
}
func (s *portalService) GetBalance(context.Context, int64, int64) (payment.Balance, error) {
	return payment.Balance{}, nil
}
func (s *portalService) ListOrders(context.Context, int64, int64, int) ([]payment.Order, error) {
	return nil, nil
}
func (s *portalService) CancelOrder(context.Context, payment.CancelOrderInput) (payment.Order, error) {
	return payment.Order{}, nil
}

// RefundOrder: 门户绝不该走资金退款路径。被调用即记账, 测试据此判用户自助退钱。
func (s *portalService) RefundOrder(_ context.Context, in payment.RefundOrderInput) (payment.RefundResult, error) {
	s.refundCalled = true
	s.refundOrderID = in.OrderID
	return payment.RefundResult{}, nil
}

func mountPortalWithSession(svc Service, d UserDeps, ident sessionauth.SessionIdentity) http.Handler {
	d.Service = svc
	inner := chi.NewRouter()
	MountPaymentUserRoutes(inner, d)
	outer := chi.NewRouter()
	outer.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(sessionauth.ContextWithSession(req.Context(), ident)))
		})
	})
	outer.Mount("/", inner)
	return outer
}

// 守 create: 用户门户建单必须造 topup 类型, 金额原样透传, 且身份取自 session 而非请求体。
// mutation: handler 漏设 OrderKind=topup (或取 body 的 tenant/user) → 捕获到的 OrderKind 非 topup / 身份错 → 红。
func TestPortalCreateTopupForcesTopupKindAndSessionIdentity(t *testing.T) {
	svc := &portalService{}
	ident := sessionauth.SessionIdentity{TenantID: 7, UserID: 42}
	router := mountPortalWithSession(svc, UserDeps{RefundRequests: NewMemoryRefundRequestRecorder()}, ident)

	body, _ := json.Marshal(portalCreateTopupRequest{AmountCents: 2500, Provider: "manual"})
	req := httptest.NewRequest(http.MethodPost, "/orders", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d want 201; body=%s", rec.Code, rec.Body.String())
	}
	if svc.gotCreate.OrderKind != payment.OrderKindTopup {
		t.Fatalf("create order_kind=%q want topup", svc.gotCreate.OrderKind)
	}
	if svc.gotCreate.TenantID != 7 || svc.gotCreate.UserID != 42 {
		t.Fatalf("create identity from body not session: tenant=%d user=%d", svc.gotCreate.TenantID, svc.gotCreate.UserID)
	}
	if svc.gotCreate.AmountCents != 2500 {
		t.Fatalf("create amount=%d want 2500", svc.gotCreate.AmountCents)
	}
	if svc.gotCreate.ProviderKind != payment.ProviderManual {
		t.Fatalf("create provider=%q want manual", svc.gotCreate.ProviderKind)
	}
	if svc.gotCreate.ActorKind != payment.ActorKindUser {
		t.Fatalf("create actor_kind=%q want user", svc.gotCreate.ActorKind)
	}
	// 渲染必须带 payment_instruction 指引。
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := resp["payment_instruction"]; !ok {
		t.Fatalf("create response missing payment_instruction: %s", rec.Body.String())
	}
}

// 守 create 金额护栏: 超出配置区间的金额必须 400, 不得建单 (服务端裁决, 非客户端报价)。
// mutation: 删掉区间校验 → 超额金额被建单 → 红。
func TestPortalCreateTopupRejectsAmountOutOfRange(t *testing.T) {
	svc := &portalService{}
	ident := sessionauth.SessionIdentity{TenantID: 7, UserID: 42}
	cfg := PortalConfig{MinTopupCents: 100, MaxTopupCents: 1000}
	router := mountPortalWithSession(svc, UserDeps{Portal: cfg, RefundRequests: NewMemoryRefundRequestRecorder()}, ident)

	body, _ := json.Marshal(portalCreateTopupRequest{AmountCents: 5000, Provider: "manual"})
	req := httptest.NewRequest(http.MethodPost, "/orders", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", rec.Code, rec.Body.String())
	}
	if svc.gotCreate.AmountCents != 0 {
		t.Fatalf("out-of-range amount should not reach CreateOrder, got=%d", svc.gotCreate.AmountCents)
	}
}

// 守归属 (核心判别): 用户只能看自己的订单详情; 别人的订单返回 404, 不泄露内容。
// mutation: 删 newPortalGetOrderHandler 里 order.UserID != ident.UserID 归属校验 → 跨用户读到别人单 → 红。
func TestPortalGetOrderRejectsCrossUser(t *testing.T) {
	// 订单属于 user 99, 但 session 是 user 42。
	svc := &portalService{getOrder: payment.Order{ID: 5, TenantID: 7, UserID: 99, AmountCents: 9999, Status: payment.StatusCompleted, OrderKind: payment.OrderKindTopup}}
	ident := sessionauth.SessionIdentity{TenantID: 7, UserID: 42}
	router := mountPortalWithSession(svc, UserDeps{RefundRequests: NewMemoryRefundRequestRecorder()}, ident)

	req := httptest.NewRequest(http.MethodGet, "/orders/5", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-user order detail status=%d want 404; body=%s", rec.Code, rec.Body.String())
	}
	// 不得泄露他人订单金额。
	if bytes.Contains(rec.Body.Bytes(), []byte("9999")) {
		t.Fatalf("cross-user detail leaked another user's order body: %s", rec.Body.String())
	}
}

// 守归属正向: 用户读自己的订单 200 且渲染订单。
func TestPortalGetOwnOrderSucceeds(t *testing.T) {
	svc := &portalService{getOrder: payment.Order{ID: 5, TenantID: 7, UserID: 42, AmountCents: 2500, Status: payment.StatusPending, OrderKind: payment.OrderKindTopup}}
	ident := sessionauth.SessionIdentity{TenantID: 7, UserID: 42}
	router := mountPortalWithSession(svc, UserDeps{RefundRequests: NewMemoryRefundRequestRecorder()}, ident)

	req := httptest.NewRequest(http.MethodGet, "/orders/5", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("own order detail status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Order orderView `json:"order"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Order.ID != 5 || resp.Order.AmountCents != 2500 {
		t.Fatalf("own order detail mismatch: %+v", resp.Order)
	}
}

// 守 money (核心判别): 用户发起退款申请只建 pending 记录, 绝不动钱 (RefundOrder 永不被调用)。
// mutation: 把 handler 改成调用 d.Service.RefundOrder → svc.refundCalled=true → 红 (用户自助退了钱)。
func TestPortalRefundRequestCreatesPendingWithoutMovingMoney(t *testing.T) {
	svc := &portalService{getOrder: payment.Order{ID: 5, TenantID: 7, UserID: 42, AmountCents: 2500, Status: payment.StatusCompleted, OrderKind: payment.OrderKindTopup}}
	ident := sessionauth.SessionIdentity{TenantID: 7, UserID: 42}
	router := mountPortalWithSession(svc, UserDeps{RefundRequests: NewMemoryRefundRequestRecorder()}, ident)

	body, _ := json.Marshal(portalRefundRequestBody{Reason: "ordered by mistake"})
	req := httptest.NewRequest(http.MethodPost, "/orders/5/refund-request", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("refund-request status=%d want 202; body=%s", rec.Code, rec.Body.String())
	}
	if svc.refundCalled {
		t.Fatalf("refund-request must NOT call RefundOrder (user cannot self-refund); called for order %d", svc.refundOrderID)
	}
	var resp struct {
		RefundRequest refundRequestView `json:"refund_request"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.RefundRequest.Status != string(RefundRequestPending) {
		t.Fatalf("refund_request status=%q want pending", resp.RefundRequest.Status)
	}
	if resp.RefundRequest.OrderID != 5 {
		t.Fatalf("refund_request order_id=%d want 5", resp.RefundRequest.OrderID)
	}
}

// 守 refund-request 归属: 不能为别人的订单申请退款 (跨用户 → 404, 且不建记录/不动钱)。
func TestPortalRefundRequestRejectsCrossUser(t *testing.T) {
	svc := &portalService{getOrder: payment.Order{ID: 5, TenantID: 7, UserID: 99, AmountCents: 2500, Status: payment.StatusCompleted, OrderKind: payment.OrderKindTopup}}
	ident := sessionauth.SessionIdentity{TenantID: 7, UserID: 42}
	router := mountPortalWithSession(svc, UserDeps{RefundRequests: NewMemoryRefundRequestRecorder()}, ident)

	req := httptest.NewRequest(http.MethodPost, "/orders/5/refund-request", bytes.NewReader([]byte("{}")))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-user refund-request status=%d want 404; body=%s", rec.Code, rec.Body.String())
	}
	if svc.refundCalled {
		t.Fatalf("cross-user refund-request must not move money")
	}
}

// 守 config: 门户配置返回金额区间 + 启用渠道 (manual/taobao)。
func TestPortalConfigReportsRangeAndProviders(t *testing.T) {
	svc := &portalService{}
	ident := sessionauth.SessionIdentity{TenantID: 7, UserID: 42}
	cfg := PortalConfig{MinTopupCents: 200, MaxTopupCents: 9000}
	router := mountPortalWithSession(svc, UserDeps{Portal: cfg, RefundRequests: NewMemoryRefundRequestRecorder()}, ident)

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("config status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Config portalConfigView `json:"config"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Config.MinTopupCents != 200 || resp.Config.MaxTopupCents != 9000 {
		t.Fatalf("config range mismatch: %+v", resp.Config)
	}
	got := map[string]bool{}
	for _, p := range resp.Config.Providers {
		got[p.Provider] = true
	}
	if !got["manual"] || !got["taobao"] {
		t.Fatalf("config missing default providers manual/taobao: %+v", resp.Config.Providers)
	}
}

// 守内存退款申请幂等: 同一订单重复申请返回既有 pending, 不重复建。
func TestMemoryRefundRequestRecorderIdempotent(t *testing.T) {
	rec := NewMemoryRefundRequestRecorder()
	in := RefundRequestInput{TenantID: 7, UserID: 42, OrderID: 5, Reason: "first"}
	first, err := rec.CreateRefundRequest(context.Background(), in)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	second, err := rec.CreateRefundRequest(context.Background(), RefundRequestInput{TenantID: 7, UserID: 42, OrderID: 5, Reason: "second"})
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("repeat refund request should be idempotent: first=%d second=%d", first.ID, second.ID)
	}
	if second.Status != RefundRequestPending {
		t.Fatalf("status=%q want pending", second.Status)
	}
}
