// HUAKAI · iKun

package subscriptionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
)

// fakePaymentOrderService 捕获订阅购买建单入参, 供判别性断言。
type fakePaymentOrderService struct {
	called bool
	got    payment.CreateOrderInput
	result payment.CreateOrderResult
	err    error
}

func (f *fakePaymentOrderService) CreateOrder(_ context.Context, in payment.CreateOrderInput) (payment.CreateOrderResult, error) {
	f.called = true
	f.got = in
	return f.result, f.err
}

// listPlansSubscriptionService 记录 ListPlans 的 onlyForSale 入参, 并可返回固定套餐集。
type listPlansSubscriptionService struct {
	fakeSubscriptionService
	gotOnlyForSale bool
	listCalled     bool
	plans          []subscription.Plan
}

func (f *listPlansSubscriptionService) ListPlans(_ context.Context, _ int64, onlyForSale bool) ([]subscription.Plan, error) {
	f.listCalled = true
	f.gotOnlyForSale = onlyForSale
	return f.plans, nil
}

func newSubUserTestRouter(d UserDeps) http.Handler {
	r := chi.NewRouter()
	r.Route("/subs", func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				ctx := sessionauth.ContextWithSession(req.Context(), sessionauth.SessionIdentity{TenantID: 5, UserID: 7})
				next.ServeHTTP(w, req.WithContext(ctx))
			})
		})
		MountSubscriptionUserRoutes(r, d)
	})
	return r
}

// 守 plans 只返在售启用套餐: user 列套餐端点必须以 onlyForSale=true 调 ListPlans。
// mutation: handler 改成 ListPlans(..., false) → gotOnlyForSale=false → 红 (会把停用/下架套餐暴露给用户)。
func TestUserListPlansOnlyForSale(t *testing.T) {
	svc := &listPlansSubscriptionService{}
	router := newSubUserTestRouter(UserDeps{Service: svc})

	req := httptest.NewRequest(http.MethodGet, "/subs/plans", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !svc.listCalled {
		t.Fatal("ListPlans not called")
	}
	if !svc.gotOnlyForSale {
		t.Fatal("user plans endpoint must call ListPlans with onlyForSale=true (never expose disabled/off-sale plans)")
	}
}

// 守购买造出 subscription 类型订单且 plan_id 对: POST /purchase 必须复用支付建一张
// OrderKind=subscription、SubscriptionPlanID=plan_id、ActorKind=user 的订单。
// mutation: handler 漏设 OrderKind=subscription → got.OrderKind != "subscription" → 红 (会建成充值单偷改余额);
// 漏设 SubscriptionPlanID → 指针 nil → 红。
func TestUserPurchaseCreatesSubscriptionOrder(t *testing.T) {
	psvc := &fakePaymentOrderService{result: payment.CreateOrderResult{
		Order: payment.Order{ID: 9, OutTradeNo: "rech_t5_abc", Status: payment.StatusPending,
			AmountCents: 1990, CurrencyCode: "USD", OrderKind: payment.OrderKindSubscription},
	}}
	subSvc := &fakeSubscriptionService{plan: subscription.Plan{ID: 42, Enabled: true, ForSale: true}}
	d := UserDeps{
		Service:    subSvc,
		Payment:    psvc,
		TradeNoGen: func(int64) (string, error) { return "rech_t5_abc", nil },
	}
	router := newSubUserTestRouter(d)

	body, _ := json.Marshal(purchaseRequest{PlanID: 42})
	req := httptest.NewRequest(http.MethodPost, "/subs/purchase", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if !psvc.called {
		t.Fatal("payment CreateOrder not called")
	}
	if psvc.got.OrderKind != payment.OrderKindSubscription {
		t.Fatalf("order_kind = %q, want subscription (purchase must create a subscription order)", psvc.got.OrderKind)
	}
	if psvc.got.SubscriptionPlanID == nil || *psvc.got.SubscriptionPlanID != 42 {
		t.Fatalf("subscription_plan_id = %v, want 42", psvc.got.SubscriptionPlanID)
	}
	if psvc.got.ActorKind != payment.ActorKindUser || psvc.got.ActorID != 7 {
		t.Fatalf("actor = %q/%d, want user/7 (self-service purchase)", psvc.got.ActorKind, psvc.got.ActorID)
	}
	if psvc.got.TenantID != 5 || psvc.got.UserID != 7 {
		t.Fatalf("tenant/user = %d/%d, want 5/7 (from session, not client)", psvc.got.TenantID, psvc.got.UserID)
	}
	if psvc.got.OutTradeNo != "rech_t5_abc" {
		t.Fatalf("out_trade_no = %q, want tenant-routable generated value", psvc.got.OutTradeNo)
	}
}

// 守不卖下架套餐: 套餐 Enabled/ForSale=false 时回 409 且绝不建单 (否则用户能买到已下架套餐)。
// mutation: handler 跳过 plan.Enabled/ForSale 预检 → psvc.called=true / 非 409 → 红。
func TestUserPurchaseRejectsOffSalePlan(t *testing.T) {
	psvc := &fakePaymentOrderService{}
	subSvc := &fakeSubscriptionService{plan: subscription.Plan{ID: 42, Enabled: true, ForSale: false}}
	d := UserDeps{
		Service:    subSvc,
		Payment:    psvc,
		TradeNoGen: func(int64) (string, error) { return "rech_t5_abc", nil },
	}
	router := newSubUserTestRouter(d)

	body, _ := json.Marshal(purchaseRequest{PlanID: 42})
	req := httptest.NewRequest(http.MethodPost, "/subs/purchase", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if psvc.called {
		t.Fatal("payment CreateOrder must NOT be called for an off-sale plan")
	}
}

// 守 me 不暴露内部字段并报 auto_renew: GET /me 当前生效订阅走用户视图 (snake_case, 无 prev/source/user_id),
// 且响应含 auto_renew 字段。mutation: handler 直返 admin 视图 → 泄露 prev_user_group → 红;
// 漏 auto_renew 字段 → 红。
func TestUserCurrentSubscriptionViewAndAutoRenew(t *testing.T) {
	now := time.Now().UTC()
	active := sampleSubscription()
	active.Status = subscription.StatusActive
	active.StartsAt = now.Add(-time.Hour)
	active.ExpiresAt = now.Add(720 * time.Hour)
	svc := &fakeListSubscriptionsService{subs: []subscription.UserSubscription{active}}
	router := newSubUserTestRouter(UserDeps{Service: svc})

	req := httptest.NewRequest(http.MethodGet, "/subs/me", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	js := rec.Body.String()
	for _, leaked := range []string{"prev_user_group", "vip-secret-prev", "assigned_by_admin_id", "\"source\"", "\"user_id\""} {
		if bytes.Contains([]byte(js), []byte(leaked)) {
			t.Fatalf("GET /me leaked internal field %q: %s", leaked, js)
		}
	}
	if !bytes.Contains([]byte(js), []byte(`"auto_renew"`)) {
		t.Fatalf("GET /me must report auto_renew flag: %s", js)
	}
	if !bytes.Contains([]byte(js), []byte(`"plan_id"`)) {
		t.Fatalf("GET /me must include current subscription plan_id: %s", js)
	}
}

// fakeListSubscriptionsService 让 ListUserSubscriptions 返回固定集 (其余方法走 fakeSubscriptionService 零实现)。
type fakeListSubscriptionsService struct {
	fakeSubscriptionService
	subs []subscription.UserSubscription
}

func (f *fakeListSubscriptionsService) ListUserSubscriptions(context.Context, int64, int64) ([]subscription.UserSubscription, error) {
	return f.subs, nil
}
