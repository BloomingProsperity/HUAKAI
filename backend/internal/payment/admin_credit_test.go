package payment

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestAdminAdjustBalanceRejectsBlankReasonBeforeStore(t *testing.T) {
	store := &adminBalanceStoreStub{}
	svc := NewService(store)

	_, err := svc.AdminAdjustBalance(context.Background(), AdminBalanceAdjustmentInput{
		TenantID:        7,
		UserID:          3,
		Amount:          decimal.RequireFromString("10.00000000"),
		CurrencyCode:    "USD",
		ActorID:         "admin-11",
		Reason:          " ",
		ExternalTradeNo: "admin-no-reason",
		Now:             time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("AdminAdjustBalance err=%v want ErrInvalidInput for blank reason", err)
	}
	if store.called {
		t.Fatalf("blank reason reached store: %+v", store.got)
	}
}

func TestAdminAdjustBalanceRejectsOversizedIdempotencyKeyBeforeStore(t *testing.T) {
	store := &adminBalanceStoreStub{}
	svc := NewService(store)

	_, err := svc.AdminAdjustBalance(context.Background(), AdminBalanceAdjustmentInput{
		TenantID:        7,
		UserID:          3,
		Amount:          decimal.RequireFromString("10.00000000"),
		CurrencyCode:    "USD",
		ActorID:         "admin-11",
		Reason:          "manual correction",
		ExternalTradeNo: strings.Repeat("a", 129),
		Now:             time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("AdminAdjustBalance err=%v want ErrInvalidInput for oversized idempotency key", err)
	}
	if store.called {
		t.Fatalf("oversized idempotency key reached store: %+v", store.got)
	}
}

type adminBalanceStoreStub struct {
	called bool
	got    AdminBalanceAdjustmentInput
}

func (s *adminBalanceStoreStub) OpenRecharge(context.Context, OpenInput) (Order, error) {
	return Order{}, ErrStoreNotConfigured
}

func (s *adminBalanceStoreStub) ApplyAdminBalanceAdjustment(_ context.Context, input AdminBalanceAdjustmentInput) (AdminBalanceAdjustmentResult, error) {
	s.called = true
	s.got = input
	return AdminBalanceAdjustmentResult{}, nil
}
