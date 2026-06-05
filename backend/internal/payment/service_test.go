// HUAKAI · iKun

package payment

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func newTestService() *Service {
	fixed := time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)
	return NewService(NewMemoryStore(), WithTestProvider(), WithClock(func() time.Time { return fixed }))
}

func TestService_CreateOrderValidation(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()
	cases := []struct {
		name string
		in   CreateOrderInput
		want error
	}{
		{"zero amount", CreateOrderInput{TenantID: 1, UserID: 1, AmountCents: 0, OutTradeNo: "t"}, ErrInvalidAmount},
		{"negative amount", CreateOrderInput{TenantID: 1, UserID: 1, AmountCents: -5, OutTradeNo: "t"}, ErrInvalidAmount},
		{"amount over cap", CreateOrderInput{TenantID: 1, UserID: 1, AmountCents: maxAmountCents + 1, OutTradeNo: "t"}, ErrInvalidAmount},
		{"no tenant", CreateOrderInput{TenantID: 0, UserID: 1, AmountCents: 10, OutTradeNo: "t"}, ErrInvalidInput},
		{"no user", CreateOrderInput{TenantID: 1, UserID: 0, AmountCents: 10, OutTradeNo: "t"}, ErrInvalidInput},
		{"non-USD currency rejected", CreateOrderInput{TenantID: 1, UserID: 1, AmountCents: 10, CurrencyCode: "JPY", OutTradeNo: "t"}, ErrUnsupportedCurrency},
		{"overlong currency rejected", CreateOrderInput{TenantID: 1, UserID: 1, AmountCents: 10, CurrencyCode: "DOLLARS", OutTradeNo: "t"}, ErrUnsupportedCurrency},
		{"empty out_trade_no rejected", CreateOrderInput{TenantID: 1, UserID: 1, AmountCents: 10, OutTradeNo: ""}, ErrInvalidInput},
		{"bad out_trade_no chars", CreateOrderInput{TenantID: 1, UserID: 1, AmountCents: 10, OutTradeNo: "bad trade no!"}, ErrInvalidInput},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := svc.CreateOrder(ctx, c.in); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestService_IdempotentReplayReturnsSameOrder(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()
	in := CreateOrderInput{TenantID: 1, UserID: 2, AmountCents: 500, OutTradeNo: "trade-1", ActorAdminID: 9}
	r1, err := svc.CreateOrder(ctx, in)
	if err != nil || r1.Idempotent {
		t.Fatalf("first create: err=%v idempotent=%v", err, r1.Idempotent)
	}
	r2, err := svc.CreateOrder(ctx, in)
	if err != nil {
		t.Fatalf("replay create: %v", err)
	}
	// 自证: 重放返回同一订单 id, 不新建第二张单。
	if !r2.Idempotent || r2.Order.ID != r1.Order.ID {
		t.Fatalf("replay should return same order: idempotent=%v id1=%d id2=%d", r2.Idempotent, r1.Order.ID, r2.Order.ID)
	}
}

func TestService_IdempotencyConflictOnFieldMismatch(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()
	base := CreateOrderInput{TenantID: 1, UserID: 2, AmountCents: 500, OutTradeNo: "trade-x", ActorAdminID: 9}
	if _, err := svc.CreateOrder(ctx, base); err != nil {
		t.Fatalf("create: %v", err)
	}
	// 同 out_trade_no 但金额不同 → 冲突, 不改旧单。
	conflict := base
	conflict.AmountCents = 999
	if _, err := svc.CreateOrder(ctx, conflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("err = %v, want ErrIdempotencyConflict", err)
	}
}

func TestService_FulfillRequiresConfirm(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()
	r, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: 1, UserID: 2, AmountCents: 500, OutTradeNo: "trade-p", ActorAdminID: 9})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Fulfill(ctx, FulfillInput{TenantID: 1, OrderID: r.Order.ID}); !errors.Is(err, ErrOrderNotFulfillable) {
		t.Fatalf("fulfill pending err = %v, want ErrOrderNotFulfillable", err)
	}
	bal, _ := svc.GetBalance(ctx, 1, 2)
	if bal.AmountCents != 0 {
		t.Fatalf("balance = %d, want 0", bal.AmountCents)
	}
}

func TestService_HappyPathCreditsAndAudits(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()
	r, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: 1, UserID: 2, AmountCents: 1500, OutTradeNo: "trade-h", ActorAdminID: 9})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	res, err := svc.AdminConfirmPaid(ctx, AdminConfirmPaidInput{TenantID: 1, OrderID: r.Order.ID, ActorAdminID: 9})
	if err != nil {
		t.Fatalf("confirm+fulfill: %v", err)
	}
	if res.Order.Status != StatusCompleted || res.BalanceCents != 1500 {
		t.Fatalf("result status=%q balance=%d, want completed/1500", res.Order.Status, res.BalanceCents)
	}
	events, _ := svc.ListAuditEvents(ctx, 1, r.Order.ID)
	got := map[string]bool{}
	for _, ev := range events {
		got[ev.EventType] = true
	}
	for _, want := range []string{AuditOrderCreated, AuditPaidConfirmed, AuditFulfillmentStarted, AuditCredited} {
		if !got[want] {
			t.Fatalf("missing audit event %q (got %v)", want, got)
		}
	}
}

func TestService_DoubleConfirmCreditsOnce(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()
	r, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: 1, UserID: 2, AmountCents: 700, OutTradeNo: "trade-d", ActorAdminID: 9})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	res1, err := svc.AdminConfirmPaid(ctx, AdminConfirmPaidInput{TenantID: 1, OrderID: r.Order.ID, ActorAdminID: 9})
	if err != nil || res1.Idempotent {
		t.Fatalf("first confirm: err=%v idempotent=%v", err, res1.Idempotent)
	}
	res2, err := svc.AdminConfirmPaid(ctx, AdminConfirmPaidInput{TenantID: 1, OrderID: r.Order.ID, ActorAdminID: 9})
	if err != nil {
		t.Fatalf("second confirm: %v", err)
	}
	// 自证: 二次确认幂等, 余额仍只增一次。
	if !res2.Idempotent || res2.BalanceCents != 700 {
		t.Fatalf("double confirm: idempotent=%v balance=%d, want true/700", res2.Idempotent, res2.BalanceCents)
	}
}

func TestService_RefundOrderSubtractsFromDerivedBalanceAndAudits(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()
	r, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: 1, UserID: 2, AmountCents: 1500, OutTradeNo: "trade-refund", ActorAdminID: 9})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	credited, err := svc.AdminConfirmPaid(ctx, AdminConfirmPaidInput{TenantID: 1, OrderID: r.Order.ID, ActorAdminID: 9})
	if err != nil {
		t.Fatalf("confirm+fulfill: %v", err)
	}
	if credited.BalanceCents != 1500 {
		t.Fatalf("pre-refund balance=%d want 1500", credited.BalanceCents)
	}

	refunded, err := svc.RefundOrder(ctx, RefundOrderInput{
		TenantID:       1,
		OrderID:        r.Order.ID,
		AmountCents:    400,
		IdempotencyKey: "refund-key-1",
		Reason:         "operator refund",
		ActorKind:      ActorKindAdmin,
		ActorID:        9,
		RequestID:      "req-refund-1",
	})
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	if refunded.Order.Status != StatusRefunded || refunded.Refund.AmountCents != 400 || refunded.BalanceCents != 1100 || refunded.Idempotent {
		t.Fatalf("refund result=%+v want status refunded, amount 400, balance 1100, non-idempotent", refunded)
	}

	bal, err := svc.GetBalance(ctx, 1, 2)
	if err != nil {
		t.Fatalf("get balance: %v", err)
	}
	// 判别性: 若 balanceLocked/UserBalanceCents 漏掉 -refund, 这里会得到 1500 而不是 1100。
	if bal.AmountCents != 1100 {
		t.Fatalf("balance after refund=%d want 1100 (credit 1500 - refund 400)", bal.AmountCents)
	}
	events, _ := svc.ListAuditEvents(ctx, 1, r.Order.ID)
	got := map[string]bool{}
	for _, ev := range events {
		got[ev.EventType] = true
	}
	if !got[AuditOrderRefunded] {
		t.Fatalf("missing refund audit event (got %v)", got)
	}
}

func TestService_RefundOrderIdempotencyKeyDoesNotDoubleDeduct(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()
	r, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: 1, UserID: 2, AmountCents: 1200, OutTradeNo: "trade-refund-dupe", ActorAdminID: 9})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.AdminConfirmPaid(ctx, AdminConfirmPaidInput{TenantID: 1, OrderID: r.Order.ID, ActorAdminID: 9}); err != nil {
		t.Fatalf("confirm+fulfill: %v", err)
	}
	first, err := svc.RefundOrder(ctx, RefundOrderInput{TenantID: 1, OrderID: r.Order.ID, AmountCents: 300, IdempotencyKey: "refund-dupe", ActorKind: ActorKindAdmin, ActorID: 9})
	if err != nil || first.Idempotent {
		t.Fatalf("first refund: res=%+v err=%v", first, err)
	}
	second, err := svc.RefundOrder(ctx, RefundOrderInput{TenantID: 1, OrderID: r.Order.ID, AmountCents: 999, IdempotencyKey: "refund-dupe", ActorKind: ActorKindAdmin, ActorID: 9})
	if err != nil {
		t.Fatalf("second refund replay: %v", err)
	}
	if !second.Idempotent || second.Refund.ID != first.Refund.ID || second.Refund.AmountCents != 300 {
		t.Fatalf("second refund should replay stored refund, got %+v first=%+v", second, first)
	}
	bal, _ := svc.GetBalance(ctx, 1, 2)
	if bal.AmountCents != 900 {
		t.Fatalf("balance after replay=%d want 900 (refund only once)", bal.AmountCents)
	}
}

func TestService_RefundOrderRejectsPendingWithoutWrites(t *testing.T) {
	svc := newTestService()
	ctx := context.Background()
	r, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: 1, UserID: 2, AmountCents: 700, OutTradeNo: "trade-refund-pending", ActorAdminID: 9})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.RefundOrder(ctx, RefundOrderInput{TenantID: 1, OrderID: r.Order.ID, AmountCents: 200, IdempotencyKey: "refund-pending", ActorKind: ActorKindAdmin, ActorID: 9}); !errors.Is(err, ErrOrderNotRefundable) {
		t.Fatalf("refund pending err=%v want ErrOrderNotRefundable", err)
	}
	bal, _ := svc.GetBalance(ctx, 1, 2)
	if bal.AmountCents != 0 {
		t.Fatalf("pending refund changed balance=%d want 0", bal.AmountCents)
	}
	events, _ := svc.ListAuditEvents(ctx, 1, r.Order.ID)
	for _, ev := range events {
		if ev.EventType == AuditOrderRefunded {
			t.Fatalf("pending refund wrote refund audit event: %+v", ev)
		}
	}
}

func TestService_ExpiredOrderNotConfirmable(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	t0 := time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)
	// 建单时 TTL 1 分钟。
	createSvc := NewService(store, WithTestProvider(), WithClock(func() time.Time { return t0 }))
	r, err := createSvc.CreateOrder(ctx, CreateOrderInput{TenantID: 1, UserID: 2, AmountCents: 500, OutTradeNo: "trade-exp", ActorAdminID: 9, ExpiresIn: time.Minute})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 过期后 (T0+2min) 确认必须被拒, 不入账。
	later := t0.Add(2 * time.Minute)
	expireSvc := NewService(store, WithClock(func() time.Time { return later }))
	if _, err := expireSvc.AdminConfirmPaid(ctx, AdminConfirmPaidInput{TenantID: 1, OrderID: r.Order.ID, ActorAdminID: 9}); !errors.Is(err, ErrOrderNotConfirmable) {
		t.Fatalf("confirm expired err = %v, want ErrOrderNotConfirmable", err)
	}
	bal, _ := expireSvc.GetBalance(ctx, 1, 2)
	if bal.AmountCents != 0 {
		t.Fatalf("balance = %d, want 0 (expired order must not credit)", bal.AmountCents)
	}
}

func TestService_ProviderUnknownRejected(t *testing.T) {
	// 未启用 test provider 时, 选 test 渠道应被拒。
	svc := NewService(NewMemoryStore())
	ctx := context.Background()
	if _, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: 1, UserID: 2, AmountCents: 100, ProviderKind: ProviderTest}); !errors.Is(err, ErrProviderUnknown) {
		t.Fatalf("err = %v, want ErrProviderUnknown", err)
	}
}

const svcCallbackSecret = "svc-callback-secret"

func newCallbackService() *Service {
	fixed := time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)
	return NewService(NewMemoryStore(), WithTestProviderSecret(svcCallbackSecret), WithClock(func() time.Time { return fixed }))
}

type subscriptionPlanPricedStore struct {
	*MemoryStore
	snapshot subscriptionPlanPriceSnapshot
}

func (s *subscriptionPlanPricedStore) GetSubscriptionPlanPriceSnapshot(_ context.Context, tenantID, planID int64) (subscriptionPlanPriceSnapshot, error) {
	if s.snapshot.TenantID != tenantID || s.snapshot.PlanID != planID {
		return subscriptionPlanPriceSnapshot{}, ErrInvalidInput
	}
	return s.snapshot, nil
}

func signCallback(t *testing.T, env testCallbackEnvelope) ([]byte, string) {
	t.Helper()
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw, SignTestCallback(svcCallbackSecret, raw)
}

// TestService_SubscriptionOrderDerivesPlanAmountAndRejectsCallerUnderpay 守钱路径:
// 订阅单最终金额必须来自 plan 快照, caller 低报 amount_cents 不能污染订单或回调校验。
// mutation: CreateOrder 对订阅单继续使用 in.AmountCents → order.AmountCents=100,
// 低额回调会越过金额校验并推进订单状态, 本测试在金额断言或 underpay 拒绝断言处变红。
func TestService_SubscriptionOrderDerivesPlanAmountAndRejectsCallerUnderpay(t *testing.T) {
	planID := int64(42)
	store := &subscriptionPlanPricedStore{
		MemoryStore: NewMemoryStore(),
		snapshot: subscriptionPlanPriceSnapshot{
			TenantID:     1,
			PlanID:       planID,
			AmountCents:  5000,
			CurrencyCode: "USD",
			Enabled:      true,
		},
	}
	fixed := time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)
	svc := NewService(store, WithTestProviderSecret(svcCallbackSecret), WithClock(func() time.Time { return fixed }))
	ctx := context.Background()

	created, err := svc.CreateOrder(ctx, CreateOrderInput{
		TenantID: 1, UserID: 2,
		AmountCents:        100,
		CurrencyCode:       "EUR",
		OutTradeNo:         "sub-plan-price",
		ProviderKind:       ProviderTest,
		OrderKind:          OrderKindSubscription,
		SubscriptionPlanID: &planID,
	})
	if err != nil {
		t.Fatalf("create subscription order: %v", err)
	}
	if created.Order.AmountCents != 5000 || created.Order.CurrencyCode != "USD" {
		t.Fatalf("subscription order money = %d %s, want plan snapshot 5000 USD", created.Order.AmountCents, created.Order.CurrencyCode)
	}

	raw, sig := signCallback(t, testCallbackEnvelope{TenantID: 1, OutTradeNo: "sub-plan-price", PaidAmountCents: 100, CurrencyCode: "USD"})
	if _, err := svc.ConfirmPaidByCallback(ctx, ProviderTest, raw, sig); !errors.Is(err, ErrCallbackRejected) {
		t.Fatalf("underpaid subscription callback err = %v, want ErrCallbackRejected", err)
	}
	got, err := svc.GetOrder(ctx, 1, created.Order.ID)
	if err != nil {
		t.Fatalf("get order after underpay: %v", err)
	}
	if got.Status != StatusPending {
		t.Fatalf("underpaid callback status = %q, want pending (must not confirm low caller amount)", got.Status)
	}
}

func TestService_ConfirmPaidByCallback_HappyPathSystemActor(t *testing.T) {
	svc := newCallbackService()
	ctx := context.Background()
	r, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: 1, UserID: 2, AmountCents: 1500, OutTradeNo: "cb-h", ProviderKind: ProviderTest})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	raw, sig := signCallback(t, testCallbackEnvelope{TenantID: 1, OutTradeNo: "cb-h", PaidAmountCents: 1500, CurrencyCode: "USD"})
	res, err := svc.ConfirmPaidByCallback(ctx, ProviderTest, raw, sig)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	if res.Order.Status != StatusCompleted || res.BalanceCents != 1500 {
		t.Fatalf("status=%q balance=%d, want completed/1500", res.Order.Status, res.BalanceCents)
	}
	// 确认归属 system (回调路径), 不是 admin。
	events, _ := svc.ListAuditEvents(ctx, 1, r.Order.ID)
	var paidActor string
	for _, ev := range events {
		if ev.EventType == AuditPaidConfirmed {
			paidActor = ev.ActorKind
		}
	}
	if paidActor != ActorKindSystem {
		t.Fatalf("paid_confirmed actor = %q, want system", paidActor)
	}
}

func TestService_ConfirmPaidByCallback_ForgedRejected(t *testing.T) {
	svc := newCallbackService()
	ctx := context.Background()
	if _, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: 1, UserID: 2, AmountCents: 1500, OutTradeNo: "cb-f", ProviderKind: ProviderTest}); err != nil {
		t.Fatalf("create: %v", err)
	}
	raw, _ := signCallback(t, testCallbackEnvelope{TenantID: 1, OutTradeNo: "cb-f", PaidAmountCents: 1500})
	// 用错密钥伪造签名。
	forged := SignTestCallback("wrong-secret", raw)
	if _, err := svc.ConfirmPaidByCallback(ctx, ProviderTest, raw, forged); !errors.Is(err, ErrCallbackUnverified) {
		t.Fatalf("err = %v, want ErrCallbackUnverified", err)
	}
	if bal, _ := svc.GetBalance(ctx, 1, 2); bal.AmountCents != 0 {
		t.Fatalf("balance = %d, want 0", bal.AmountCents)
	}
}

func TestService_ConfirmPaidByCallback_AmountMismatchRejected(t *testing.T) {
	svc := newCallbackService()
	ctx := context.Background()
	if _, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: 1, UserID: 2, AmountCents: 5000, OutTradeNo: "cb-a", ProviderKind: ProviderTest}); err != nil {
		t.Fatalf("create: %v", err)
	}
	raw, sig := signCallback(t, testCallbackEnvelope{TenantID: 1, OutTradeNo: "cb-a", PaidAmountCents: 4900, CurrencyCode: "USD"})
	if _, err := svc.ConfirmPaidByCallback(ctx, ProviderTest, raw, sig); !errors.Is(err, ErrCallbackRejected) {
		t.Fatalf("err = %v, want ErrCallbackRejected", err)
	}
	if bal, _ := svc.GetBalance(ctx, 1, 2); bal.AmountCents != 0 {
		t.Fatalf("balance = %d, want 0", bal.AmountCents)
	}
}

func TestService_ConfirmPaidByCallback_ManualProviderNoCallback(t *testing.T) {
	svc := newCallbackService()
	ctx := context.Background()
	// manual provider 不实现 CallbackVerifier。
	raw, sig := signCallback(t, testCallbackEnvelope{TenantID: 1, OutTradeNo: "cb-m", PaidAmountCents: 100})
	if _, err := svc.ConfirmPaidByCallback(ctx, ProviderManual, raw, sig); !errors.Is(err, ErrProviderNoCallback) {
		t.Fatalf("err = %v, want ErrProviderNoCallback", err)
	}
}

func TestService_ConfirmPaidByCallback_UnknownProvider(t *testing.T) {
	// 未启用 test provider 的 service, test 渠道回调应 ErrProviderUnknown。
	svc := NewService(NewMemoryStore())
	ctx := context.Background()
	if _, err := svc.ConfirmPaidByCallback(ctx, ProviderTest, []byte(`{}`), "sig"); !errors.Is(err, ErrProviderUnknown) {
		t.Fatalf("err = %v, want ErrProviderUnknown", err)
	}
}
