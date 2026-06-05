// HUAKAI · iKun

package payment

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestService_AdminListOrdersFiltersStatusTimeAndPaginates(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	tenantA, tenantB, userA := int64(1), int64(2), int64(10)
	day1 := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC)
	day3 := time.Date(2026, 6, 3, 9, 0, 0, 0, time.UTC)

	pendingOld := createAt(t, ctx, store, day1, tenantA, userA, 100, "pending-old", false)
	pendingNew := createAt(t, ctx, store, day2, tenantA, userA, 200, "pending-new", false)
	_ = createAt(t, ctx, store, day2, tenantA, userA, 300, "completed-a", true)
	_ = createAt(t, ctx, store, day2, tenantB, userA, 400, "pending-b", false)

	filter := OrderListFilter{
		TenantID: tenantA,
		Status:   StatusPending,
		From:     &day1,
		To:       &day3,
		Limit:    1,
		Offset:   1,
	}
	got, err := NewService(store).AdminListOrders(ctx, filter)
	if err != nil {
		t.Fatalf("AdminListOrders: %v", err)
	}
	if len(got) != 1 || got[0].ID != pendingOld.ID {
		t.Fatalf("filtered page ids=%v want only older pending order %d after newest pending %d", orderIDs(got), pendingOld.ID, pendingNew.ID)
	}
}

func TestService_DashboardStatsCountsTenantScopedOrders(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	tenantA, tenantB, userA := int64(1), int64(2), int64(10)
	day1 := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC)
	day3 := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)

	_ = createAt(t, ctx, store, day1, tenantA, userA, 100, "stats-a-1", false)
	_ = createAt(t, ctx, store, day2, tenantA, userA, 200, "stats-a-2", true)
	_ = createAt(t, ctx, store, day2, tenantB, userA, 900, "stats-b", false)

	stats, err := NewService(store, WithClock(func() time.Time { return day2.Add(time.Hour) })).DashboardStats(ctx, DashboardFilter{
		TenantID: tenantA,
		From:     day1,
		To:       day3,
	})
	if err != nil {
		t.Fatalf("DashboardStats: %v", err)
	}
	if stats.TotalAmountCents != 300 || stats.TotalCount != 2 || stats.TodayCount != 1 || stats.AverageAmountCents != 150 {
		t.Fatalf("stats=%+v want total_amount=300 total_count=2 today_count=1 average=150", stats)
	}
	if len(stats.DailySeries) != 2 {
		t.Fatalf("daily series len=%d want 2: %+v", len(stats.DailySeries), stats.DailySeries)
	}
	if stats.DailySeries[0].Date != "2026-06-01" || stats.DailySeries[0].OrderCount != 1 || stats.DailySeries[0].AmountCents != 100 {
		t.Fatalf("day1 series=%+v want 2026-06-01 count=1 amount=100", stats.DailySeries[0])
	}
	if stats.DailySeries[1].Date != "2026-06-02" || stats.DailySeries[1].OrderCount != 1 || stats.DailySeries[1].AmountCents != 200 {
		t.Fatalf("day2 series=%+v want 2026-06-02 count=1 amount=200", stats.DailySeries[1])
	}
}

func TestService_RetryFulfillmentCompletesOnceAndCompletedReplayIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	svc := NewService(store, WithClock(func() time.Time {
		return time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	}))
	created, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: 1, UserID: 2, AmountCents: 700, OutTradeNo: "retry-once", ActorAdminID: 9})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.ConfirmPaid(ctx, confirmRecord{TenantID: 1, OrderID: created.Order.ID, AdminID: 9, Now: time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatalf("confirm paid: %v", err)
	}
	if _, _, err := store.BeginFulfill(ctx, fulfillRecord{TenantID: 1, OrderID: created.Order.ID, ActorKind: ActorKindAdmin, ActorID: 9, Now: time.Date(2026, 6, 2, 12, 1, 0, 0, time.UTC)}); err != nil {
		t.Fatalf("begin fulfill crash point: %v", err)
	}

	first, err := svc.RetryFulfillment(ctx, RetryFulfillmentInput{TenantID: 1, OrderID: created.Order.ID, ActorAdminID: 9, RequestID: "retry-1"})
	if err != nil {
		t.Fatalf("first retry: %v", err)
	}
	if first.Idempotent || first.BalanceCents != 700 || first.Order.Status != StatusCompleted {
		t.Fatalf("first retry result=%+v want non-idempotent completed balance=700", first)
	}
	second, err := svc.RetryFulfillment(ctx, RetryFulfillmentInput{TenantID: 1, OrderID: created.Order.ID, ActorAdminID: 9, RequestID: "retry-2"})
	if err != nil {
		t.Fatalf("second retry: %v", err)
	}
	if !second.Idempotent || second.BalanceCents != 700 {
		t.Fatalf("completed retry result=%+v want idempotent balance=700", second)
	}
	bal, err := svc.GetBalance(ctx, 1, 2)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if bal.AmountCents != 700 {
		t.Fatalf("balance=%d want 700; retry must not double-credit", bal.AmountCents)
	}
}

func TestService_SetProviderRuntimeConfigTogglesTaobao(t *testing.T) {
	ctx := context.Background()
	svc := NewService(NewMemoryStore())
	if _, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: 1, UserID: 2, AmountCents: 100, OutTradeNo: "taobao-disabled", ProviderKind: ProviderTaobao}); !errors.Is(err, ErrProviderUnknown) {
		t.Fatalf("disabled taobao create err=%v want ErrProviderUnknown", err)
	}

	cfg, err := svc.SetProviderRuntimeConfig(ctx, ProviderRuntimeConfigInput{
		ProviderKind: ProviderTaobao,
		Enabled:      true,
		CheckoutURL:  "https://pay.example/taobao",
		UpdatedBy:    "99",
	})
	if err != nil {
		t.Fatalf("enable taobao config: %v", err)
	}
	if !cfg.Enabled || cfg.CheckoutURL != "https://pay.example/taobao" {
		t.Fatalf("cfg=%+v want enabled checkout_url", cfg)
	}
	if _, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: 1, UserID: 2, AmountCents: 100, OutTradeNo: "taobao-enabled", ProviderKind: ProviderTaobao}); err != nil {
		t.Fatalf("enabled taobao create: %v", err)
	}

	if _, err := svc.SetProviderRuntimeConfig(ctx, ProviderRuntimeConfigInput{ProviderKind: ProviderTaobao, Enabled: false, UpdatedBy: "99"}); err != nil {
		t.Fatalf("disable taobao config: %v", err)
	}
	if _, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: 1, UserID: 2, AmountCents: 100, OutTradeNo: "taobao-disabled-again", ProviderKind: ProviderTaobao}); !errors.Is(err, ErrProviderUnknown) {
		t.Fatalf("disabled-again taobao create err=%v want ErrProviderUnknown", err)
	}
}

func createAt(t *testing.T, ctx context.Context, store *MemoryStore, at time.Time, tenantID, userID, amount int64, trade string, complete bool) Order {
	t.Helper()
	svc := NewService(store, WithTestProvider(), WithClock(func() time.Time { return at }))
	res, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: tenantID, UserID: userID, AmountCents: amount, OutTradeNo: trade, ActorAdminID: 9})
	if err != nil {
		t.Fatalf("create %s: %v", trade, err)
	}
	if complete {
		if _, err := svc.AdminConfirmPaid(ctx, AdminConfirmPaidInput{TenantID: tenantID, OrderID: res.Order.ID, ActorAdminID: 9}); err != nil {
			t.Fatalf("complete %s: %v", trade, err)
		}
		res.Order, err = svc.GetOrder(ctx, tenantID, res.Order.ID)
		if err != nil {
			t.Fatalf("get completed %s: %v", trade, err)
		}
	}
	return res.Order
}

func orderIDs(orders []Order) []int64 {
	out := make([]int64, 0, len(orders))
	for _, order := range orders {
		out = append(out, order.ID)
	}
	return out
}
