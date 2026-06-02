package subscription

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestCreatePlanRejectsZeroPriceForPaymentBackedOrderFlow(t *testing.T) {
	svc := NewService(&storeStub{}, nil)
	_, err := svc.CreatePlan(context.Background(), PlanInput{
		TenantID:         7,
		Code:             "free",
		Name:             "Free",
		Price:            decimal.Zero,
		CurrencyCode:     "USD",
		DurationUnit:     DurationMonth,
		DurationValue:    1,
		QuotaResetPeriod: ResetNever,
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("CreatePlan err=%v want ErrInvalidInput for zero price", err)
	}
}

func TestCreatePlanRejectsInvalidDurationAndResetPolicy(t *testing.T) {
	svc := NewService(&storeStub{}, nil)
	for _, tc := range []struct {
		name  string
		input PlanInput
	}{
		{
			name: "duration typo",
			input: PlanInput{
				TenantID:         7,
				Code:             "bad-duration",
				Name:             "Bad Duration",
				Price:            decimal.RequireFromString("1.00000000"),
				CurrencyCode:     "USD",
				DurationUnit:     DurationUnit("wek"),
				DurationValue:    1,
				QuotaResetPeriod: ResetNever,
			},
		},
		{
			name: "reset typo",
			input: PlanInput{
				TenantID:         7,
				Code:             "bad-reset",
				Name:             "Bad Reset",
				Price:            decimal.RequireFromString("1.00000000"),
				CurrencyCode:     "USD",
				DurationUnit:     DurationMonth,
				DurationValue:    1,
				QuotaResetPeriod: ResetPeriod("montly"),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.CreatePlan(context.Background(), tc.input)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("CreatePlan err=%v want ErrInvalidInput", err)
			}
		})
	}
}

type storeStub struct{}

func (s *storeStub) CreatePlan(context.Context, PlanInput) (Plan, error) {
	return Plan{ID: 1}, nil
}

func (s *storeStub) ListPlans(context.Context, int64, bool) ([]Plan, error) {
	return nil, nil
}

func (s *storeStub) GetPlan(context.Context, int64, int64) (Plan, error) {
	return Plan{}, ErrPlanNotFound
}

func (s *storeStub) UpdatePlan(context.Context, PlanPatch) (Plan, error) {
	return Plan{}, nil
}

func (s *storeStub) ArchivePlan(context.Context, int64, int64, time.Time) error {
	return nil
}

func (s *storeStub) CreateOrder(context.Context, createOrderRecord) (Order, error) {
	return Order{}, nil
}

func (s *storeStub) CancelRechargeOrder(context.Context, int64, int64, time.Time) error {
	return nil
}

func (s *storeStub) ActivatePaidOrder(context.Context, ActivatePaidOrderInput) (ActivationResult, error) {
	return ActivationResult{}, nil
}

func (s *storeStub) ListUserSubscriptions(context.Context, ListUserSubscriptionsInput) ([]UserSubscription, error) {
	return nil, nil
}

func (s *storeStub) ExpireDueSubscriptions(context.Context, ExpireDueInput) (int, error) {
	return 0, nil
}

func (s *storeStub) ResetDueSubscriptions(context.Context, ResetDueInput) (int, error) {
	return 0, nil
}
