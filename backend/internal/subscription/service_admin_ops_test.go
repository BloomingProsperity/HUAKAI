// HUAKAI · iKun

package subscription

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestService_UpdatePlanValidation(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc, _ := newTestService(&now)
	ctx := context.Background()
	plan, err := svc.CreatePlan(ctx, CreatePlanInput{
		TenantID: 1, Name: "basic", ValidityDays: 30, GrantedGroup: "premium",
		MonthlyCapUSD: capPtr("10"), ForSale: true,
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}

	if _, err := svc.UpdatePlan(ctx, UpdatePlanInput{
		TenantID: 1, PlanID: plan.ID, Name: "bad", ValidityDays: 30,
		GrantedGroup: "premium", PriceCents: -1, MonthlyCapUSD: capPtr("10"),
		ForSale: true, ActorAdminID: 7,
	}); !errors.Is(err, ErrPlanInvalid) {
		t.Fatalf("negative price err=%v, want ErrPlanInvalid", err)
	}

	updated, err := svc.UpdatePlan(ctx, UpdatePlanInput{
		TenantID: 1, PlanID: plan.ID, Name: "premium-new", Description: "ops",
		PriceCents: 2500, CurrencyCode: "usd", ValidityDays: 45, GrantedGroup: "premium",
		DailyCapUSD: capPtr("2"), MonthlyCapUSD: capPtr("20"), ForSale: false, SortOrder: 9,
		ActorAdminID: 7, RequestID: "plan-update-1",
	})
	if err != nil {
		t.Fatalf("update plan: %v", err)
	}
	if updated.Name != "premium-new" || updated.PriceCents != 2500 || updated.CurrencyCode != "USD" ||
		updated.ValidityDays != 45 || updated.ForSale || updated.SortOrder != 9 {
		t.Fatalf("updated plan fields mismatch: %+v", updated)
	}
	if updated.DailyCapUSD == nil || !updated.DailyCapUSD.Equal(*capPtr("2")) ||
		updated.MonthlyCapUSD == nil || !updated.MonthlyCapUSD.Equal(*capPtr("20")) {
		t.Fatalf("updated cap baseline mismatch: daily=%v monthly=%v", updated.DailyCapUSD, updated.MonthlyCapUSD)
	}
}

func TestService_BulkAssignPartialFailure(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc, store := newTestService(&now)
	ctx := context.Background()
	store.seedUser(1, 42, "default")
	store.seedUser(1, 43, "default")
	plan, err := svc.CreatePlan(ctx, CreatePlanInput{
		TenantID: 1, Name: "premium", ValidityDays: 30, GrantedGroup: "premium", MonthlyCapUSD: capPtr("10"),
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}

	// 让本测试变红的变异:把所有用户包进同一个事务,并在用户 999 缺失时整体回滚。
	result, err := svc.BulkAssign(ctx, BulkAssignInput{
		TenantID: 1, UserIDs: []int64{42, 999, 43}, PlanID: plan.ID, ActorAdminID: 7, RequestID: "bulk-1",
	})
	if err != nil {
		t.Fatalf("bulk assign should collect per-user errors, got top-level err: %v", err)
	}
	if len(result.Results) != 3 {
		t.Fatalf("result count=%d, want 3", len(result.Results))
	}
	if !result.Results[0].OK || result.Results[0].Subscription.UserID != 42 {
		t.Fatalf("user 42 result mismatch: %+v", result.Results[0])
	}
	if result.Results[1].OK || result.Results[1].Error == "" {
		t.Fatalf("missing user result should be per-user err: %+v", result.Results[1])
	}
	if !result.Results[2].OK || result.Results[2].Subscription.UserID != 43 {
		t.Fatalf("user 43 result mismatch: %+v", result.Results[2])
	}
	for _, userID := range []int64{42, 43} {
		subs, err := svc.ListUserSubscriptions(ctx, 1, userID)
		if err != nil {
			t.Fatalf("list user %d subs: %v", userID, err)
		}
		if len(subs) != 1 || subs[0].Status != StatusActive {
			t.Fatalf("persisted subs for user %d = %+v, want one active", userID, subs)
		}
	}
}

func TestService_ExtendSubscriptionIdempotentByRequestID(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc, store := newTestService(&now)
	ctx := context.Background()
	store.seedUser(1, 42, "default")
	plan, _ := svc.CreatePlan(ctx, CreatePlanInput{TenantID: 1, Name: "p", ValidityDays: 30, GrantedGroup: "premium"})
	assigned, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: 1, UserID: 42, PlanID: plan.ID, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	original := assigned.Subscription.ExpiresAt

	// 让本测试变红的变异:忽略此前的审计 request_id,每次重试都再加 30 天。
	first, err := svc.ExtendSubscription(ctx, ExtendSubscriptionInput{
		TenantID: 1, SubscriptionID: assigned.Subscription.ID, ActorAdminID: 7, RequestID: "extend-once", Days: 30,
	})
	if err != nil {
		t.Fatalf("first extend: %v", err)
	}
	second, err := svc.ExtendSubscription(ctx, ExtendSubscriptionInput{
		TenantID: 1, SubscriptionID: assigned.Subscription.ID, ActorAdminID: 7, RequestID: "extend-once", Days: 30,
	})
	if err != nil {
		t.Fatalf("second extend: %v", err)
	}
	want := original.AddDate(0, 0, 30)
	if !first.ExpiresAt.Equal(want) || !second.ExpiresAt.Equal(want) {
		t.Fatalf("expires first=%v second=%v, want exactly once %v", first.ExpiresAt, second.ExpiresAt, want)
	}
}

func TestService_ChangePlanDowngradeGuard(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc, store := newTestService(&now)
	ctx := context.Background()
	store.seedUser(1, 42, "default")
	high, err := svc.CreatePlan(ctx, CreatePlanInput{
		TenantID: 1, Name: "high", ValidityDays: 30, GrantedGroup: "premium", MonthlyCapUSD: capPtr("20"),
	})
	if err != nil {
		t.Fatalf("create high plan: %v", err)
	}
	low, err := svc.CreatePlan(ctx, CreatePlanInput{
		TenantID: 1, Name: "low", ValidityDays: 30, GrantedGroup: "premium", MonthlyCapUSD: capPtr("5"),
	})
	if err != nil {
		t.Fatalf("create low plan: %v", err)
	}
	assigned, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{
		TenantID: 1, UserID: 42, PlanID: high.ID, ActorAdminID: 7,
	})
	if err != nil {
		t.Fatalf("assign high: %v", err)
	}

	// 让本测试变红的变异:无视 AllowDowngrade=false,仍然套用更低的上限。
	if _, err := svc.ChangePlan(ctx, ChangePlanInput{
		TenantID: 1, SubscriptionID: assigned.Subscription.ID, NewPlanID: low.ID,
		AllowDowngrade: false, ActorAdminID: 7, RequestID: "change-denied",
	}); !errors.Is(err, ErrDowngradeNotAllowed) {
		t.Fatalf("disallowed downgrade err=%v, want ErrDowngradeNotAllowed", err)
	}
	unchanged, err := svc.GetSubscription(ctx, 1, assigned.Subscription.ID)
	if err != nil {
		t.Fatalf("get unchanged subscription: %v", err)
	}
	if unchanged.PlanID != high.ID || unchanged.MonthlyCapUSD == nil || !unchanged.MonthlyCapUSD.Equal(*capPtr("20")) {
		t.Fatalf("subscription changed after denied downgrade: %+v", unchanged)
	}

	changed, err := svc.ChangePlan(ctx, ChangePlanInput{
		TenantID: 1, SubscriptionID: assigned.Subscription.ID, NewPlanID: low.ID,
		AllowDowngrade: true, ActorAdminID: 7, RequestID: "change-allowed",
	})
	if err != nil {
		t.Fatalf("allowed downgrade: %v", err)
	}
	if changed.PlanID != low.ID || changed.MonthlyCapUSD == nil || !changed.MonthlyCapUSD.Equal(*capPtr("5")) {
		t.Fatalf("changed subscription = %+v, want low plan cap", changed)
	}
}

func TestService_ChangePlanIdempotentByRequestID(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc, store := newTestService(&now)
	ctx := context.Background()
	store.seedUser(1, 42, "default")
	basic, _ := svc.CreatePlan(ctx, CreatePlanInput{
		TenantID: 1, Name: "basic", ValidityDays: 30, GrantedGroup: "premium", MonthlyCapUSD: capPtr("10"),
	})
	premium, _ := svc.CreatePlan(ctx, CreatePlanInput{
		TenantID: 1, Name: "premium", ValidityDays: 30, GrantedGroup: "premium", MonthlyCapUSD: capPtr("30"),
	})
	assigned, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{
		TenantID: 1, UserID: 42, PlanID: basic.ID, ActorAdminID: 7,
	})
	if err != nil {
		t.Fatalf("assign basic: %v", err)
	}
	originalExpires := assigned.Subscription.ExpiresAt

	// 让本测试变红的变异:丢掉 request_id 审计重放,每次重试都续期/安装上限。
	for i := 0; i < 2; i++ {
		if _, err := svc.ChangePlan(ctx, ChangePlanInput{
			TenantID: 1, SubscriptionID: assigned.Subscription.ID, NewPlanID: premium.ID,
			ActorAdminID: 7, RequestID: "change-once",
		}); err != nil {
			t.Fatalf("change attempt %d: %v", i+1, err)
		}
	}
	got, err := svc.GetSubscription(ctx, 1, assigned.Subscription.ID)
	if err != nil {
		t.Fatalf("get changed subscription: %v", err)
	}
	wantExpires := originalExpires.AddDate(0, 0, 30)
	if got.PlanID != premium.ID || !got.ExpiresAt.Equal(wantExpires) {
		t.Fatalf("changed subscription plan/expires = plan %d expires %v, want plan %d expires %v",
			got.PlanID, got.ExpiresAt, premium.ID, wantExpires)
	}
	events, err := svc.ListAuditEvents(ctx, 1, assigned.Subscription.ID)
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	renewed := 0
	for _, ev := range events {
		if ev.EventType == AuditSubscriptionRenewed {
			renewed++
		}
	}
	if renewed != 1 {
		t.Fatalf("subscription_renewed audit count=%d, want 1", renewed)
	}
}

func TestService_ChangePlanRejectsNonActive(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc, store := newTestService(&now)
	ctx := context.Background()
	store.seedUser(1, 42, "default")
	basic, _ := svc.CreatePlan(ctx, CreatePlanInput{
		TenantID: 1, Name: "basic", ValidityDays: 30, GrantedGroup: "premium", MonthlyCapUSD: capPtr("10"),
	})
	premium, _ := svc.CreatePlan(ctx, CreatePlanInput{
		TenantID: 1, Name: "premium", ValidityDays: 30, GrantedGroup: "premium", MonthlyCapUSD: capPtr("30"),
	})
	assigned, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{
		TenantID: 1, UserID: 42, PlanID: basic.ID, ActorAdminID: 7,
	})
	if err != nil {
		t.Fatalf("assign basic: %v", err)
	}
	if _, err := svc.CancelSubscription(ctx, 1, assigned.Subscription.ID, 7, "cancel-before-change", "admin_token:7"); err != nil {
		t.Fatalf("cancel before change: %v", err)
	}

	// 让本测试变红的变异:仅按 id 更新,而不加 status/expires_at 护栏。
	if _, err := svc.ChangePlan(ctx, ChangePlanInput{
		TenantID: 1, SubscriptionID: assigned.Subscription.ID, NewPlanID: premium.ID,
		ActorAdminID: 7, RequestID: "change-cancelled",
	}); !errors.Is(err, ErrSubscriptionNotActive) {
		t.Fatalf("change cancelled err=%v, want ErrSubscriptionNotActive", err)
	}
}
