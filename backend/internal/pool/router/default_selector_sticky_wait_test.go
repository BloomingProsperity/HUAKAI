package router

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

type slotUnavailableIDs map[int64]struct{}

func (s slotUnavailableIDs) Acquire(_ context.Context, account *AccountSnapshot, _ SelectionRequest) (*AcquireResult, error) {
	if account != nil {
		if _, blocked := s[account.ID]; blocked {
			return nil, ErrNoSlotAvailable
		}
	}
	return &AcquireResult{AcquisitionToken: uuid.New()}, nil
}

func TestSelectStickySlotFullWaitsWhenBudgetSet(t *testing.T) {
	accounts := []*AccountSnapshot{
		{ID: 101, TenantID: 7, Priority: 1, LoadRate: 0.01, MaxConcurrency: 4, HealthState: "healthy"},
		{ID: 202, TenantID: 7, Priority: 50, LoadRate: 0.01, MaxConcurrency: 4, HealthState: "healthy"},
	}
	sel := NewDefaultSelector(&stubAccountSource{accounts: accounts},
		WithSlotManager(slotUnavailableIDs{202: {}}),
		WithClaimGate(&captureClaimGate{}),
		WithStickyStore(&stubSticky{bindings: map[string]int64{"sess-wait": 202}}),
		WithRoutingPolicySource(&stubPolicy{p: &RoutingPolicy{StickyTimeoutMS: 1500, StickyMaxWaiting: 2}}),
	)
	res, err := sel.Select(context.Background(), SelectionRequest{
		TenantID: 7, ClaimID: 88, RequestedModel: "m", SessionHash: "sess-wait",
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if res == nil || res.WaitPlan == nil {
		t.Fatalf("粘号仅槽满时应返回 WaitPlan, got %+v", res)
	}
	if res.WaitPlan.AccountID != 202 || res.WaitPlan.TimeoutMS != 1500 || res.WaitPlan.MaxWaiting != 2 {
		t.Fatalf("WaitPlan=%+v want sticky 202 / 1500 / 2", res.WaitPlan)
	}
	if res.AccountID != 0 {
		t.Fatalf("等待期间不得改选其他号, got account=%d", res.AccountID)
	}
}

func TestSelectStickySlotFullBreaksWhenBudgetZero(t *testing.T) {
	accounts := []*AccountSnapshot{
		{ID: 101, TenantID: 7, Priority: 1, LoadRate: 0.01, MaxConcurrency: 4, HealthState: "healthy"},
		{ID: 202, TenantID: 7, Priority: 50, LoadRate: 0.01, MaxConcurrency: 4, HealthState: "healthy"},
	}
	sel := NewDefaultSelector(&stubAccountSource{accounts: accounts},
		WithSlotManager(slotUnavailableIDs{202: {}}),
		WithClaimGate(&captureClaimGate{}),
		WithStickyStore(&stubSticky{bindings: map[string]int64{"sess-break": 202}}),
		WithRoutingPolicySource(&stubPolicy{p: &RoutingPolicy{}}),
	)
	res, err := sel.Select(context.Background(), SelectionRequest{
		TenantID: 7, ClaimID: 89, RequestedModel: "m", SessionHash: "sess-break",
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if res == nil || res.AccountID != 101 {
		t.Fatalf("sticky wait 为 0 时应立刻改选 101, got %+v", res)
	}
	if res.WaitPlan != nil {
		t.Fatalf("预算为 0 不得进入 WaitPlan: %+v", res.WaitPlan)
	}
}

func TestSelectStickyHealthBreakDoesNotWait(t *testing.T) {
	accounts := []*AccountSnapshot{
		{ID: 101, TenantID: 7, Priority: 1, LoadRate: 0.01, MaxConcurrency: 4, HealthState: "healthy"},
		{ID: 202, TenantID: 7, Priority: 50, LoadRate: 0.01, MaxConcurrency: 4, HealthState: HealthStateCoolingDown},
	}
	sel := NewDefaultSelector(&stubAccountSource{accounts: accounts},
		WithSlotManager(newMemSlotManager()),
		WithClaimGate(&captureClaimGate{}),
		WithStickyStore(&stubSticky{bindings: map[string]int64{"sess-cool": 202}}),
		WithRoutingPolicySource(&stubPolicy{p: &RoutingPolicy{StickyTimeoutMS: 1500, StickyMaxWaiting: 2}}),
	)
	res, err := sel.Select(context.Background(), SelectionRequest{
		TenantID: 7, ClaimID: 90, RequestedModel: "m", SessionHash: "sess-cool",
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if res == nil || res.AccountID != 101 {
		t.Fatalf("粘号被健康门挡下时不得排队, got %+v", res)
	}
	if res.WaitPlan != nil {
		t.Fatalf("健康打断不得 WaitPlan: %+v", res.WaitPlan)
	}
}

func TestStickyWaitPlanRequiresBudget(t *testing.T) {
	account := &AccountSnapshot{ID: 9, MaxConcurrency: 3}
	if plan := stickyWaitPlan(account, nil); plan != nil {
		t.Fatalf("nil policy 不得排队: %+v", plan)
	}
	if plan := stickyWaitPlan(account, &RoutingPolicy{}); plan != nil {
		t.Fatalf("零预算不得排队: %+v", plan)
	}
	plan := stickyWaitPlan(account, &RoutingPolicy{StickyTimeoutMS: 10})
	if plan == nil || plan.AccountID != 9 || plan.TimeoutMS != 10 {
		t.Fatalf("仅 timeout 也应排队: %+v", plan)
	}
}
