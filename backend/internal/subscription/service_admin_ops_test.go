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

	// MUTATION that makes this RED: wrap all users in one transaction and roll back when user 999 is missing.
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

	// MUTATION that makes this RED: ignore prior audit request_id and add 30 days on every retry.
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
