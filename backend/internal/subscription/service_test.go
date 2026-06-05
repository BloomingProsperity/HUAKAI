// HUAKAI · iKun

package subscription

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func capPtr(s string) *decimal.Decimal {
	d := decimal.RequireFromString(s)
	return &d
}

func newTestService(clk *time.Time) (*Service, *memoryStore) {
	store := newMemoryStore()
	svc := NewService(store, WithClock(func() time.Time { return *clk }))
	return svc, store
}

func TestService_CreatePlanValidation(t *testing.T) {
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	svc, _ := newTestService(&now)
	ctx := context.Background()

	cases := []struct {
		name string
		in   CreatePlanInput
		want error
	}{
		{"empty name", CreatePlanInput{TenantID: 1, ValidityDays: 30, GrantedGroup: "premium"}, ErrInvalidInput},
		{"zero validity", CreatePlanInput{TenantID: 1, Name: "p", ValidityDays: 0, GrantedGroup: "premium"}, ErrPlanInvalid},
		{"over-max validity", CreatePlanInput{TenantID: 1, Name: "p", ValidityDays: maxValidityDays + 1, GrantedGroup: "premium"}, ErrPlanInvalid},
		{"negative cap", CreatePlanInput{TenantID: 1, Name: "p", ValidityDays: 30, GrantedGroup: "premium", MonthlyCapUSD: capPtr("-1")}, ErrPlanInvalid},
		{"no group no cap", CreatePlanInput{TenantID: 1, Name: "p", ValidityDays: 30}, ErrPlanInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.CreatePlan(ctx, tc.in); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
		})
	}

	// 合法: 有组无 cap, 或有 cap 无组, 都应通过。
	if _, err := svc.CreatePlan(ctx, CreatePlanInput{TenantID: 1, Name: "ok-group", ValidityDays: 30, GrantedGroup: "premium"}); err != nil {
		t.Fatalf("valid group-only plan rejected: %v", err)
	}
	if _, err := svc.CreatePlan(ctx, CreatePlanInput{TenantID: 1, Name: "ok-cap", ValidityDays: 30, MonthlyCapUSD: capPtr("10")}); err != nil {
		t.Fatalf("valid cap-only plan rejected: %v", err)
	}
}

func TestService_AssignRequiresExistingUserAndPlan(t *testing.T) {
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	svc, store := newTestService(&now)
	ctx := context.Background()
	plan, err := svc.CreatePlan(ctx, CreatePlanInput{TenantID: 1, Name: "p", ValidityDays: 30, GrantedGroup: "premium"})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	// 用户未 seed → ErrInvalidInput (镜像 PG FK/FOR UPDATE 缺行)。
	if _, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: 1, UserID: 99, PlanID: plan.ID}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("assign to missing user: err=%v, want ErrInvalidInput", err)
	}
	// plan 不存在。
	store.seedUser(1, 99, "default")
	if _, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: 1, UserID: 99, PlanID: 9999}); !errors.Is(err, ErrPlanNotFound) {
		t.Fatalf("assign with missing plan: err=%v, want ErrPlanNotFound", err)
	}
}

func TestService_AssignIdempotentByGroup(t *testing.T) {
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	svc, store := newTestService(&now)
	ctx := context.Background()
	store.seedUser(1, 42, "default")
	plan, _ := svc.CreatePlan(ctx, CreatePlanInput{TenantID: 1, Name: "p", ValidityDays: 30, GrantedGroup: "premium"})

	r1, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: 1, UserID: 42, PlanID: plan.ID})
	if err != nil || r1.Idempotent {
		t.Fatalf("first assign: err=%v idempotent=%v", err, r1.Idempotent)
	}
	r2, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: 1, UserID: 42, PlanID: plan.ID})
	if err != nil || !r2.Idempotent || r2.Subscription.ID != r1.Subscription.ID {
		t.Fatalf("second assign should be idempotent same sub: err=%v idempotent=%v", err, r2.Idempotent)
	}
	subs, _ := svc.ListUserSubscriptions(ctx, 1, 42)
	active := 0
	for _, s := range subs {
		if s.Status == StatusActive {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("active subscriptions = %d, want 1", active)
	}
}

func TestService_SetAutoRenewUpdatesCurrentActiveSubscription(t *testing.T) {
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	svc, store := newTestService(&now)
	ctx := context.Background()
	store.seedUser(1, 42, "default")
	plan, _ := svc.CreatePlan(ctx, CreatePlanInput{TenantID: 1, Name: "p", ValidityDays: 30, GrantedGroup: "premium"})
	assigned, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: 1, UserID: 42, PlanID: plan.ID})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if !assigned.Subscription.AutoRenew {
		t.Fatal("new active subscription AutoRenew = false, want default true")
	}

	updated, err := svc.SetAutoRenew(ctx, 1, 42, false)
	if err != nil {
		t.Fatalf("set auto_renew=false: %v", err)
	}
	if updated.ID != assigned.Subscription.ID {
		t.Fatalf("updated subscription id = %d, want %d", updated.ID, assigned.Subscription.ID)
	}
	if updated.AutoRenew {
		t.Fatal("updated AutoRenew = true, want false")
	}
	if updated.Status != StatusActive {
		t.Fatalf("status = %q, want active (cancel-renew must not cancel entitlement)", updated.Status)
	}
	if !updated.ExpiresAt.Equal(assigned.Subscription.ExpiresAt) {
		t.Fatalf("expires_at changed from %v to %v; cancel-renew must preserve active term", assigned.Subscription.ExpiresAt, updated.ExpiresAt)
	}
	persisted, err := svc.GetSubscription(ctx, 1, assigned.Subscription.ID)
	if err != nil {
		t.Fatalf("get persisted subscription: %v", err)
	}
	if persisted.AutoRenew {
		t.Fatal("persisted AutoRenew = true, want false (SetAutoRenew must write through store)")
	}
}

func TestService_SetAutoRenewRejectsCrossUserNoOp(t *testing.T) {
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	svc, store := newTestService(&now)
	ctx := context.Background()
	store.seedUser(1, 42, "default")
	store.seedUser(1, 43, "default")
	plan, _ := svc.CreatePlan(ctx, CreatePlanInput{TenantID: 1, Name: "p", ValidityDays: 30, GrantedGroup: "premium"})
	assigned, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: 1, UserID: 42, PlanID: plan.ID})
	if err != nil {
		t.Fatalf("assign user 42: %v", err)
	}

	if _, err := svc.SetAutoRenew(ctx, 1, 43, false); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("set auto_renew for user without active subscription err=%v, want ErrSubscriptionNotFound", err)
	}
	persisted, err := svc.GetSubscription(ctx, 1, assigned.Subscription.ID)
	if err != nil {
		t.Fatalf("get user 42 subscription: %v", err)
	}
	if !persisted.AutoRenew {
		t.Fatal("cross-user no-op changed user 42 AutoRenew to false")
	}
}

func TestService_ExpiryDowngradeGuard(t *testing.T) {
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	svc, store := newTestService(&now)
	ctx := context.Background()
	store.seedUser(1, 42, "default")
	premium, _ := svc.CreatePlan(ctx, CreatePlanInput{TenantID: 1, Name: "premium", ValidityDays: 30, GrantedGroup: "premium"})
	premium2, _ := svc.CreatePlan(ctx, CreatePlanInput{TenantID: 1, Name: "premium2", ValidityDays: 30, GrantedGroup: "premium2"})

	sub1, _ := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: 1, UserID: 42, PlanID: premium.ID})
	svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: 1, UserID: 42, PlanID: premium2.ID})
	if g := store.userGroupOf(userKey{1, 42}); g != "premium2" {
		t.Fatalf("group after second upgrade = %q, want premium2", g)
	}
	// 到期旧 premium 订阅, 守卫应保留 premium2 (current != sub1.granted_group)。
	if _, err := store.ExpireSubscription(ctx, lifecycleRecord{TenantID: 1, SubscriptionID: sub1.Subscription.ID, ActorKind: ActorKindSystem, Now: now}); err != nil {
		t.Fatalf("expire sub1: %v", err)
	}
	if g := store.userGroupOf(userKey{1, 42}); g != "premium2" {
		t.Fatalf("group after expiring older sub = %q, want premium2 (guard)", g)
	}
}

func TestService_ChainedExpiryResolvesToDefault(t *testing.T) {
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	svc, store := newTestService(&now)
	ctx := context.Background()
	store.seedUser(1, 42, "default")
	basic, _ := svc.CreatePlan(ctx, CreatePlanInput{TenantID: 1, Name: "basic", ValidityDays: 30, GrantedGroup: "basic"})
	premium, _ := svc.CreatePlan(ctx, CreatePlanInput{TenantID: 1, Name: "premium", ValidityDays: 30, GrantedGroup: "premium"})

	subBasic, _ := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: 1, UserID: 42, PlanID: basic.ID})
	subPremium, _ := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: 1, UserID: 42, PlanID: premium.ID})
	if g := store.userGroupOf(userKey{1, 42}); g != "premium" {
		t.Fatalf("after chain = %q, want premium", g)
	}
	// 到期 basic: 守卫跳过 (current=premium)。
	store.ExpireSubscription(ctx, lifecycleRecord{TenantID: 1, SubscriptionID: subBasic.Subscription.ID, ActorKind: ActorKindSystem, Now: now})
	if g := store.userGroupOf(userKey{1, 42}); g != "premium" {
		t.Fatalf("after expire basic = %q, want premium", g)
	}
	// 到期 premium: 无剩余 active → default (不得回已到期 basic)。
	store.ExpireSubscription(ctx, lifecycleRecord{TenantID: 1, SubscriptionID: subPremium.Subscription.ID, ActorKind: ActorKindSystem, Now: now})
	if g := store.userGroupOf(userKey{1, 42}); g != "default" {
		t.Fatalf("after chained expiry = %q, want default", g)
	}
}

func TestExpiryWorker_TickOnceDrainsDue(t *testing.T) {
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	svc, store := newTestService(&now)
	ctx := context.Background()
	store.seedUser(1, 42, "default")
	plan, _ := svc.CreatePlan(ctx, CreatePlanInput{TenantID: 1, Name: "p", ValidityDays: 30, GrantedGroup: "premium"})
	sub, _ := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: 1, UserID: 42, PlanID: plan.ID})

	worker := NewExpiryWorker(ExpiryWorkerConfig{Service: svc, BatchSize: 10})
	// 未到期: tick 无操作。
	worker.TickOnce(ctx)
	if got, _ := svc.GetSubscription(ctx, 1, sub.Subscription.ID); got.Status != StatusActive {
		t.Fatalf("status before expiry = %v, want active", got.Status)
	}
	// 推进到过期后: tick 应到期它 + 降级。
	now = now.AddDate(0, 0, 31)
	worker.TickOnce(ctx)
	if got, _ := svc.GetSubscription(ctx, 1, sub.Subscription.ID); got.Status != StatusExpired {
		t.Fatalf("status after expiry tick = %v, want expired", got.Status)
	}
	if g := store.userGroupOf(userKey{1, 42}); g != "default" {
		t.Fatalf("group after expiry = %q, want default", g)
	}
	if worker.ExpiredTotal() != 1 {
		t.Fatalf("expired total = %d, want 1", worker.ExpiredTotal())
	}
}
