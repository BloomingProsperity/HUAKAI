// HUAKAI · iKun

package payment

import (
	"context"
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
