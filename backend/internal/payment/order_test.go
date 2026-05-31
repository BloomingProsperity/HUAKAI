package payment

import (
	"context"
	"errors"
	"testing"

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

type recordingStore struct {
	calls int
}

func (s *recordingStore) OpenRecharge(context.Context, OpenInput) (Order, error) {
	s.calls++
	return Order{}, nil
}

func fixedUnitTradeNo(value string) ExternalTradeNoGenerator {
	return func(context.Context) (string, error) {
		return value, nil
	}
}
