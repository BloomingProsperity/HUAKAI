package payment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestOpenRechargeRejectsAmountBeyondMoneyScaleBeforeStore(t *testing.T) {
	store := &recordingStore{}
	svc := NewService(store, WithExternalTradeNoGenerator(fixedUnitTradeNo("scale-too-deep")))

	_, err := svc.OpenRecharge(context.Background(), OpenInput{
		TenantID:          1,
		UserID:            2,
		Amount:            decimal.RequireFromString("50.000000001"),
		CurrencyCode:      "USD",
		MaxPendingPerUser: 3,
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err=%v want ErrInvalidInput for amount beyond numeric(20,8)", err)
	}
	if store.calls != 0 {
		t.Fatalf("store calls=%d want 0; invalid money precision must be rejected before persistence", store.calls)
	}
}

func TestOpenRechargeRejectsAmountBeyondMoneyIntegerCapacityBeforeStore(t *testing.T) {
	store := &recordingStore{}
	svc := NewService(store, WithExternalTradeNoGenerator(fixedUnitTradeNo("scale-too-wide")))

	_, err := svc.OpenRecharge(context.Background(), OpenInput{
		TenantID:          1,
		UserID:            2,
		Amount:            decimal.RequireFromString("1000000000000.00000000"),
		CurrencyCode:      "USD",
		MaxPendingPerUser: 3,
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err=%v want ErrInvalidInput for amount beyond numeric(20,8)", err)
	}
	if store.calls != 0 {
		t.Fatalf("store calls=%d want 0; oversized money amount must be rejected before persistence", store.calls)
	}
}

func TestOpenRechargeRejectsNonUSDUntilCapsAreCurrencyAware(t *testing.T) {
	store := &recordingStore{}
	svc := NewService(store, WithExternalTradeNoGenerator(fixedUnitTradeNo("non-usd")))

	_, err := svc.OpenRecharge(context.Background(), OpenInput{
		TenantID:          1,
		UserID:            2,
		Amount:            decimal.RequireFromString("50.00000000"),
		CurrencyCode:      "EUR",
		MaxPendingPerUser: 3,
		DailyAmountLimit:  decimal.RequireFromString("500.00000000"),
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err=%v want ErrInvalidInput for non-USD recharge before currency-aware caps", err)
	}
	if store.calls != 0 {
		t.Fatalf("store calls=%d want 0; non-USD must not reach USD-only limit accounting", store.calls)
	}
}

func TestOpenRechargePendingLimitCountsBeyondListPage(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC)
	svc := NewService(store, WithTestProvider(), WithClock(func() time.Time { return now }), WithExternalTradeNoGenerator(fixedUnitTradeNo("pending-new")))
	for i := 0; i < 501; i++ {
		if _, err := svc.CreateOrder(ctx, CreateOrderInput{
			TenantID: 1, UserID: 2, AmountCents: 100, OutTradeNo: "pending-" + stringID(i),
			ProviderKind: ProviderTest,
		}); err != nil {
			t.Fatalf("seed pending order %d: %v", i, err)
		}
	}

	_, err := svc.OpenRecharge(ctx, OpenInput{
		TenantID: 1, UserID: 2, Provider: "test", Amount: decimal.RequireFromString("1.00"),
		CurrencyCode: "USD", MaxPendingPerUser: 501, Now: now,
	})
	if !errors.Is(err, ErrPendingLimit) {
		t.Fatalf("OpenRecharge err=%v want ErrPendingLimit with 501 pending orders beyond page limit", err)
	}
}

func TestOpenRechargeDailyLimitCountsBeyondListPage(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC)
	svc := NewService(store, WithTestProvider(), WithClock(func() time.Time { return now }), WithExternalTradeNoGenerator(fixedUnitTradeNo("daily-new")))
	for i := 0; i < 501; i++ {
		if _, err := svc.CreateOrder(ctx, CreateOrderInput{
			TenantID: 1, UserID: 2, AmountCents: 100, OutTradeNo: "daily-" + stringID(i),
			ProviderKind: ProviderTest,
		}); err != nil {
			t.Fatalf("seed daily order %d: %v", i, err)
		}
	}

	_, err := svc.OpenRecharge(ctx, OpenInput{
		TenantID: 1, UserID: 2, Provider: "test", Amount: decimal.RequireFromString("1.00"),
		CurrencyCode: "USD", MaxPendingPerUser: 1000, DailyAmountLimit: decimal.RequireFromString("501.00"), Now: now,
	})
	if !errors.Is(err, ErrDailyAmountLimit) {
		t.Fatalf("OpenRecharge err=%v want ErrDailyAmountLimit with 501 same-day orders beyond page limit", err)
	}
}

func TestOpenRechargeAuditsUserActor(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	svc := NewService(store, WithTestProvider(), WithExternalTradeNoGenerator(fixedUnitTradeNo("user-actor")))
	res, err := svc.OpenRecharge(ctx, OpenInput{
		TenantID: 1, UserID: 22, Provider: "test", Amount: decimal.RequireFromString("1.00"),
		CurrencyCode: "USD", MaxPendingPerUser: 5,
	})
	if err != nil {
		t.Fatalf("OpenRecharge: %v", err)
	}
	events, err := svc.ListAuditEvents(ctx, 1, res.Order.ID)
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(events) == 0 || events[0].EventType != AuditOrderCreated {
		t.Fatalf("events=%+v want first order_created", events)
	}
	if events[0].ActorKind != ActorKindUser || events[0].ActorID != 22 {
		t.Fatalf("order_created actor=%s/%d want user/22", events[0].ActorKind, events[0].ActorID)
	}
}

type recordingStore struct {
	*MemoryStore
	calls int
}

func (s *recordingStore) CreateOrder(ctx context.Context, rec createOrderRecord) (Order, bool, error) {
	s.calls++
	if s.MemoryStore == nil {
		s.MemoryStore = NewMemoryStore()
	}
	return s.MemoryStore.CreateOrder(ctx, rec)
}

func fixedUnitTradeNo(value string) ExternalTradeNoGenerator {
	return func(context.Context) (string, error) {
		return value, nil
	}
}

func stringID(i int) string {
	return decimal.NewFromInt(int64(i)).String()
}
