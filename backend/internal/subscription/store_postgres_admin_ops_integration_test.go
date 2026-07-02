// HUAKAI · iKun
//go:build integration_pg

package subscription

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/shopspring/decimal"
)

func TestSubscriptionPostgres_AdminExtendSubscription(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	store := NewPostgresStore(pool)
	svc := NewService(store, WithClock(clk.now))
	plan := createPremiumPlan(t, ctx, svc, f.tenantA, "premium")

	active, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("assign active: %v", err)
	}
	original := active.Subscription.ExpiresAt
	// 让其变红的变异:去掉 active/非过期的守卫,允许 cancelled/expired 的行被延期。
	extended, err := svc.ExtendSubscription(ctx, ExtendSubscriptionInput{
		TenantID: f.tenantA, SubscriptionID: active.Subscription.ID, ActorAdminID: 7, RequestID: "extend-active", Days: 30,
	})
	if err != nil {
		t.Fatalf("extend active: %v", err)
	}
	if want := original.AddDate(0, 0, 30); !extended.ExpiresAt.Equal(want) {
		t.Fatalf("extended expires_at=%v, want %v", extended.ExpiresAt, want)
	}

	cancelled, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA2, PlanID: plan.ID, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("assign cancelled candidate: %v", err)
	}
	if _, err := svc.CancelSubscription(ctx, f.tenantA, cancelled.Subscription.ID, 7, "cancel-before-extend", "admin_token:7"); err != nil {
		t.Fatalf("cancel candidate: %v", err)
	}
	if _, err := svc.ExtendSubscription(ctx, ExtendSubscriptionInput{
		TenantID: f.tenantA, SubscriptionID: cancelled.Subscription.ID, ActorAdminID: 7, RequestID: "extend-cancelled", Days: 30,
	}); !errors.Is(err, ErrSubscriptionNotActive) {
		t.Fatalf("extend cancelled err=%v, want ErrSubscriptionNotActive", err)
	}

	expiredUser := f.seedUser(f.tenantA, "expired")
	expired, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: expiredUser, PlanID: plan.ID, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("assign expired candidate: %v", err)
	}
	if _, err := store.ExpireSubscription(ctx, lifecycleRecord{
		TenantID: f.tenantA, SubscriptionID: expired.Subscription.ID, ActorKind: ActorKindSystem, Now: clk.now(),
	}); err != nil {
		t.Fatalf("expire candidate: %v", err)
	}
	if _, err := svc.ExtendSubscription(ctx, ExtendSubscriptionInput{
		TenantID: f.tenantA, SubscriptionID: expired.Subscription.ID, ActorAdminID: 7, RequestID: "extend-expired", Days: 30,
	}); !errors.Is(err, ErrSubscriptionNotActive) {
		t.Fatalf("extend expired err=%v, want ErrSubscriptionNotActive", err)
	}
}

func TestSubscriptionPostgres_AdminExtendIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	plan := createPremiumPlan(t, ctx, svc, f.tenantA, "premium")

	assigned, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	original := assigned.Subscription.ExpiresAt
	// 让其变红的变异:去掉按 request_id 的幂等查找,导致重复延期。
	for i := 0; i < 2; i++ {
		if _, err := svc.ExtendSubscription(ctx, ExtendSubscriptionInput{
			TenantID: f.tenantA, SubscriptionID: assigned.Subscription.ID, ActorAdminID: 7, RequestID: "extend-once", Days: 30,
		}); err != nil {
			t.Fatalf("extend attempt %d: %v", i+1, err)
		}
	}
	got, err := svc.GetSubscription(ctx, f.tenantA, assigned.Subscription.ID)
	if err != nil {
		t.Fatalf("get subscription: %v", err)
	}
	if want := original.AddDate(0, 0, 30); !got.ExpiresAt.Equal(want) {
		t.Fatalf("expires_at=%v, want %v exactly once", got.ExpiresAt, want)
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_audit_events
		WHERE tenant_id=$1 AND user_subscription_id=$2 AND event_type=$3`,
		f.tenantA, assigned.Subscription.ID, AuditSubscriptionExtended); n != 1 {
		t.Fatalf("subscription_extended audit count=%d, want 1", n)
	}
}

func TestSubscriptionPostgres_AdminResetQuota(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	plan := createPremiumPlan(t, ctx, svc, f.tenantA, "premium")

	assigned, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	oldPolicy := f.activeSubPolicyID(assigned.Subscription.ID, string(CapWindowMonthly))
	f.seedQuotaWindow(oldPolicy, clk.now(), "4", "3", 2)

	// 让其变红的变异:重置到错误的基准额度,或仍挂着旧的已消耗 active 窗口。
	if _, err := svc.ResetQuota(ctx, ResetQuotaInput{
		TenantID: f.tenantA, SubscriptionID: assigned.Subscription.ID, ActorAdminID: 7, RequestID: "reset-quota-1",
	}); err != nil {
		t.Fatalf("reset quota: %v", err)
	}
	if enabled := f.countInt(`SELECT count(*) FROM quota_policies WHERE tenant_id=$1 AND id=$2 AND enabled=true`, f.tenantA, oldPolicy); enabled != 0 {
		t.Fatalf("old policy enabled count=%d, want 0", enabled)
	}
	newPolicy := f.activeSubPolicyID(assigned.Subscription.ID, string(CapWindowMonthly))
	if newPolicy == oldPolicy {
		t.Fatalf("reset reused old policy id=%d; want fresh policy without consumed window", newPolicy)
	}
	if got := f.policyLimit(newPolicy); !got.Equal(decimal.NewFromInt(10)) {
		t.Fatalf("active policy limit=%s, want plan baseline 10", got.String())
	}
	if n := f.countInt(`SELECT count(*) FROM quota_windows WHERE tenant_id=$1 AND policy_id=$2`, f.tenantA, newPolicy); n != 0 {
		t.Fatalf("active policy consumed windows=%d, want 0 after reset", n)
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_audit_events
		WHERE tenant_id=$1 AND user_subscription_id=$2 AND event_type=$3`,
		f.tenantA, assigned.Subscription.ID, AuditSubscriptionQuotaReset); n != 1 {
		t.Fatalf("subscription_quota_reset audit count=%d, want 1", n)
	}
}

func TestSubscriptionPostgres_ChangePlanReconcilesCapsInPlace(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	basic, err := svc.CreatePlan(ctx, CreatePlanInput{
		TenantID: f.tenantA, Name: "basic", ValidityDays: 30, GrantedGroup: "premium", MonthlyCapUSD: dec("10"),
	})
	if err != nil {
		t.Fatalf("create basic plan: %v", err)
	}
	premium, err := svc.CreatePlan(ctx, CreatePlanInput{
		TenantID: f.tenantA, Name: "premium", ValidityDays: 30, GrantedGroup: "premium", MonthlyCapUSD: dec("25"),
	})
	if err != nil {
		t.Fatalf("create premium plan: %v", err)
	}
	assigned, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{
		TenantID: f.tenantA, UserID: f.userA, PlanID: basic.ID, ActorAdminID: 7,
	})
	if err != nil {
		t.Fatalf("assign basic: %v", err)
	}
	oldPolicy := f.activeSubPolicyID(assigned.Subscription.ID, string(CapWindowMonthly))

	// 模拟当期已用 $7:给旧 monthly policy 写一条 quota_windows。换套餐(期中)绝不能把它清零,
	// 否则自助 /change-plan 成了绕过月度护栏白吃成本的第二扇门(与 ActivateOrRenewTx 同源)。
	f.seedQuotaWindow(oldPolicy, clk.now(), "0", "7", 1)

	changed, err := svc.ChangePlan(ctx, ChangePlanInput{
		TenantID: f.tenantA, SubscriptionID: assigned.Subscription.ID, NewPlanID: premium.ID,
		AllowDowngrade: false, ActorAdminID: 7, RequestID: "change-swap",
	})
	if err != nil {
		t.Fatalf("change plan: %v", err)
	}
	if changed.PlanID != premium.ID || changed.MonthlyCapUSD == nil || !changed.MonthlyCapUSD.Equal(*dec("25")) {
		t.Fatalf("changed subscription = %+v, want premium snapshot", changed)
	}
	// 期中换套餐 = 原地 reconcile:同一 policy_id、仍 enabled、limit 升到 25,不留 disabled 旧策略。
	newPolicy := f.activeSubPolicyID(assigned.Subscription.ID, string(CapWindowMonthly))
	if newPolicy != oldPolicy {
		t.Fatalf("期中换套餐应原地复用同一 policy_id(否则用量被重置), old=%d new=%d", oldPolicy, newPolicy)
	}
	if active := f.countInt(`SELECT count(*) FROM subscription_policy_links
		WHERE tenant_id=$1 AND user_subscription_id=$2 AND status='active'`,
		f.tenantA, assigned.Subscription.ID); active != 1 {
		t.Fatalf("active policy link count=%d, want 1", active)
	}
	if disabled := f.countInt(`SELECT count(*) FROM quota_policies WHERE tenant_id=$1 AND id=$2 AND enabled=false`, f.tenantA, oldPolicy); disabled != 0 {
		t.Fatalf("期中换套餐不应 disable 旧 policy, 实际 %d", disabled)
	}
	if got := f.policyLimit(newPolicy); !got.Equal(decimal.NewFromInt(25)) {
		t.Fatalf("active policy limit=%s, want 25 (原地升档)", got.String())
	}
	// 核心:已用量 settled_value=7 必须保留(护栏不被清零)。
	if n := f.countInt(`SELECT count(*) FROM quota_windows WHERE tenant_id=$1 AND policy_id=$2 AND settled_value=7`, f.tenantA, oldPolicy); n != 1 {
		t.Fatalf("换套餐后已用量应保留 settled_value=7, 命中 %d 行", n)
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_audit_events
		WHERE tenant_id=$1 AND user_subscription_id=$2 AND event_type=$3`,
		f.tenantA, assigned.Subscription.ID, AuditSubscriptionRenewed); n != 1 {
		t.Fatalf("subscription_renewed audit count=%d, want 1", n)
	}
}

func TestSubscriptionPostgres_ChangePlanDowngradeGuard(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	high, err := svc.CreatePlan(ctx, CreatePlanInput{
		TenantID: f.tenantA, Name: "high", ValidityDays: 30, GrantedGroup: "premium", MonthlyCapUSD: dec("20"),
	})
	if err != nil {
		t.Fatalf("create high plan: %v", err)
	}
	low, err := svc.CreatePlan(ctx, CreatePlanInput{
		TenantID: f.tenantA, Name: "low", ValidityDays: 30, GrantedGroup: "premium", MonthlyCapUSD: dec("5"),
	})
	if err != nil {
		t.Fatalf("create low plan: %v", err)
	}
	assigned, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{
		TenantID: f.tenantA, UserID: f.userA, PlanID: high.ID, ActorAdminID: 7,
	})
	if err != nil {
		t.Fatalf("assign high: %v", err)
	}
	oldPolicy := f.activeSubPolicyID(assigned.Subscription.ID, string(CapWindowMonthly))

	// 让其变红的变异:忽略守卫,在 AllowDowngrade=false 时仍允许更低额度。
	if _, err := svc.ChangePlan(ctx, ChangePlanInput{
		TenantID: f.tenantA, SubscriptionID: assigned.Subscription.ID, NewPlanID: low.ID,
		AllowDowngrade: false, ActorAdminID: 7, RequestID: "downgrade-denied",
	}); !errors.Is(err, ErrDowngradeNotAllowed) {
		t.Fatalf("disallowed downgrade err=%v, want ErrDowngradeNotAllowed", err)
	}
	unchanged, err := svc.GetSubscription(ctx, f.tenantA, assigned.Subscription.ID)
	if err != nil {
		t.Fatalf("get unchanged: %v", err)
	}
	if unchanged.PlanID != high.ID {
		t.Fatalf("plan after denied downgrade=%d, want high plan %d", unchanged.PlanID, high.ID)
	}
	if active := f.activeSubPolicyID(assigned.Subscription.ID, string(CapWindowMonthly)); active != oldPolicy {
		t.Fatalf("active policy after denied downgrade=%d, want original %d", active, oldPolicy)
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_audit_events
		WHERE tenant_id=$1 AND user_subscription_id=$2 AND event_type=$3`,
		f.tenantA, assigned.Subscription.ID, AuditSubscriptionRenewed); n != 0 {
		t.Fatalf("subscription_renewed audit count after denied downgrade=%d, want 0", n)
	}

	if _, err := svc.ChangePlan(ctx, ChangePlanInput{
		TenantID: f.tenantA, SubscriptionID: assigned.Subscription.ID, NewPlanID: low.ID,
		AllowDowngrade: true, ActorAdminID: 7, RequestID: "downgrade-allowed",
	}); err != nil {
		t.Fatalf("allowed downgrade: %v", err)
	}
	got, err := svc.GetSubscription(ctx, f.tenantA, assigned.Subscription.ID)
	if err != nil {
		t.Fatalf("get downgraded: %v", err)
	}
	if got.PlanID != low.ID || got.MonthlyCapUSD == nil || !got.MonthlyCapUSD.Equal(*dec("5")) {
		t.Fatalf("downgraded subscription=%+v, want low plan snapshot", got)
	}
}

func TestSubscriptionPostgres_ChangePlanIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	basic, err := svc.CreatePlan(ctx, CreatePlanInput{
		TenantID: f.tenantA, Name: "basic", ValidityDays: 30, GrantedGroup: "premium", MonthlyCapUSD: dec("10"),
	})
	if err != nil {
		t.Fatalf("create basic plan: %v", err)
	}
	premium, err := svc.CreatePlan(ctx, CreatePlanInput{
		TenantID: f.tenantA, Name: "premium", ValidityDays: 30, GrantedGroup: "premium", MonthlyCapUSD: dec("30"),
	})
	if err != nil {
		t.Fatalf("create premium plan: %v", err)
	}
	assigned, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{
		TenantID: f.tenantA, UserID: f.userA, PlanID: basic.ID, ActorAdminID: 7,
	})
	if err != nil {
		t.Fatalf("assign basic: %v", err)
	}
	originalExpires := assigned.Subscription.ExpiresAt

	// 让其变红的变异:去掉按 request_id 的幂等查找,导致重复续费和多余的审计。
	for i := 0; i < 2; i++ {
		if _, err := svc.ChangePlan(ctx, ChangePlanInput{
			TenantID: f.tenantA, SubscriptionID: assigned.Subscription.ID, NewPlanID: premium.ID,
			AllowDowngrade: false, ActorAdminID: 7, RequestID: "change-once",
		}); err != nil {
			t.Fatalf("change attempt %d: %v", i+1, err)
		}
	}
	got, err := svc.GetSubscription(ctx, f.tenantA, assigned.Subscription.ID)
	if err != nil {
		t.Fatalf("get subscription: %v", err)
	}
	if want := originalExpires.AddDate(0, 0, 30); !got.ExpiresAt.Equal(want) {
		t.Fatalf("expires_at=%v, want %v exactly once", got.ExpiresAt, want)
	}
	if active := f.countInt(`SELECT count(*) FROM subscription_policy_links
		WHERE tenant_id=$1 AND user_subscription_id=$2 AND status='active'`,
		f.tenantA, assigned.Subscription.ID); active != 1 {
		t.Fatalf("active policy link count=%d, want 1", active)
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_audit_events
		WHERE tenant_id=$1 AND user_subscription_id=$2 AND event_type=$3`,
		f.tenantA, assigned.Subscription.ID, AuditSubscriptionRenewed); n != 1 {
		t.Fatalf("subscription_renewed audit count=%d, want 1", n)
	}
}

func TestSubscriptionPostgres_ChangePlanRejectsNonActive(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	store := NewPostgresStore(pool)
	svc := NewService(store, WithClock(clk.now))
	plan := createPremiumPlan(t, ctx, svc, f.tenantA, "premium")
	upgrade, err := svc.CreatePlan(ctx, CreatePlanInput{
		TenantID: f.tenantA, Name: "upgrade", ValidityDays: 30, GrantedGroup: "premium", MonthlyCapUSD: dec("25"),
	})
	if err != nil {
		t.Fatalf("create upgrade plan: %v", err)
	}
	cancelled, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{
		TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID, ActorAdminID: 7,
	})
	if err != nil {
		t.Fatalf("assign cancelled candidate: %v", err)
	}
	if _, err := svc.CancelSubscription(ctx, f.tenantA, cancelled.Subscription.ID, 7, "cancel-before-change", "admin_token:7"); err != nil {
		t.Fatalf("cancel candidate: %v", err)
	}
	// 让其变红的变异:不带 active 状态守卫就更新 cancelled/expired 的行。
	if _, err := svc.ChangePlan(ctx, ChangePlanInput{
		TenantID: f.tenantA, SubscriptionID: cancelled.Subscription.ID, NewPlanID: upgrade.ID,
		ActorAdminID: 7, RequestID: "change-cancelled",
	}); !errors.Is(err, ErrSubscriptionNotActive) {
		t.Fatalf("change cancelled err=%v, want ErrSubscriptionNotActive", err)
	}

	expiredUser := f.seedUser(f.tenantA, "expired-change")
	expired, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{
		TenantID: f.tenantA, UserID: expiredUser, PlanID: plan.ID, ActorAdminID: 7,
	})
	if err != nil {
		t.Fatalf("assign expired candidate: %v", err)
	}
	if _, err := store.ExpireSubscription(ctx, lifecycleRecord{
		TenantID: f.tenantA, SubscriptionID: expired.Subscription.ID, ActorKind: ActorKindSystem, Now: clk.now(),
	}); err != nil {
		t.Fatalf("expire candidate: %v", err)
	}
	if _, err := svc.ChangePlan(ctx, ChangePlanInput{
		TenantID: f.tenantA, SubscriptionID: expired.Subscription.ID, NewPlanID: upgrade.ID,
		ActorAdminID: 7, RequestID: "change-expired",
	}); !errors.Is(err, ErrSubscriptionNotActive) {
		t.Fatalf("change expired err=%v, want ErrSubscriptionNotActive", err)
	}
}

func TestSubscriptionPostgres_AdminBulkAssignPartialFailure(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	plan := createPremiumPlan(t, ctx, svc, f.tenantA, "premium")
	missingUser := int64(999999999)

	// 让其变红的变异:把所有 assignment 包进一个事务,一旦失败就把两个有效用户也一并回滚。
	result, err := svc.BulkAssign(ctx, BulkAssignInput{
		TenantID: f.tenantA, UserIDs: []int64{f.userA, missingUser, f.userA2}, PlanID: plan.ID,
		ActorAdminID: 7, RequestID: "bulk-partial",
	})
	if err != nil {
		t.Fatalf("bulk assign top-level err=%v, want per-user collection", err)
	}
	if len(result.Results) != 3 {
		t.Fatalf("result count=%d, want 3", len(result.Results))
	}
	if !result.Results[0].OK || result.Results[0].Subscription.UserID != f.userA {
		t.Fatalf("userA result mismatch: %+v", result.Results[0])
	}
	if result.Results[1].OK || result.Results[1].Error == "" {
		t.Fatalf("missing user result should carry err: %+v", result.Results[1])
	}
	if !result.Results[2].OK || result.Results[2].Subscription.UserID != f.userA2 {
		t.Fatalf("userA2 result mismatch: %+v", result.Results[2])
	}
	if n := f.countInt(`SELECT count(*) FROM user_subscriptions
		WHERE tenant_id=$1 AND user_id IN ($2, $3) AND status='active'`,
		f.tenantA, f.userA, f.userA2); n != 2 {
		t.Fatalf("persisted active assignments=%d, want 2 despite one failure", n)
	}
}

func TestSubscriptionPostgres_AdminRevokeSubscription(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	plan := createPremiumPlan(t, ctx, svc, f.tenantA, "premium")

	assigned, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	scope := strconv.FormatInt(f.userA, 10)
	// 让其变红的变异:撤销时不关闭 quota-policy,留下仍 enabled 的权益护栏处于 active。
	revoked, err := svc.RevokeSubscription(ctx, RevokeSubscriptionInput{
		TenantID: f.tenantA, SubscriptionID: assigned.Subscription.ID, ActorAdminID: 7,
		Reason: "fraud", RequestID: "revoke-1",
	})
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if revoked.Status != StatusRevoked {
		t.Fatalf("status=%q, want revoked", revoked.Status)
	}
	if g := f.userGroup(f.tenantA, f.userA); g != DefaultUserGroup {
		t.Fatalf("user group=%q, want default after revoke", g)
	}
	if n := f.countInt(`SELECT count(*) FROM quota_policies
		WHERE tenant_id=$1 AND scope_id=$2 AND enabled=true`, f.tenantA, scope); n != 0 {
		t.Fatalf("enabled policy count after revoke=%d, want 0", n)
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_policy_links
		WHERE tenant_id=$1 AND user_subscription_id=$2 AND status='active'`,
		f.tenantA, assigned.Subscription.ID); n != 0 {
		t.Fatalf("active policy links after revoke=%d, want 0", n)
	}
	again, err := svc.RevokeSubscription(ctx, RevokeSubscriptionInput{
		TenantID: f.tenantA, SubscriptionID: assigned.Subscription.ID, ActorAdminID: 7,
		Reason: "fraud", RequestID: "revoke-1",
	})
	if err != nil {
		t.Fatalf("idempotent re-revoke: %v", err)
	}
	if again.Status != StatusRevoked {
		t.Fatalf("second status=%q, want revoked", again.Status)
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_audit_events
		WHERE tenant_id=$1 AND user_subscription_id=$2 AND event_type=$3`,
		f.tenantA, assigned.Subscription.ID, AuditSubscriptionRevoked); n != 1 {
		t.Fatalf("subscription_revoked audit count=%d, want 1", n)
	}
}

func TestSubscriptionPostgres_AdminUpdatePlan(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	plan := createPremiumPlan(t, ctx, svc, f.tenantA, "premium")

	updated, err := svc.UpdatePlan(ctx, UpdatePlanInput{
		TenantID: f.tenantA, PlanID: plan.ID, Name: "premium-plus", Description: "ops updated",
		PriceCents: 2999, CurrencyCode: "USD", ValidityDays: 60, GrantedGroup: "premium",
		DailyCapUSD: dec("3"), MonthlyCapUSD: dec("25"), ForSale: false, SortOrder: 11,
		ActorAdminID: 7, RequestID: "plan-update-1",
	})
	if err != nil {
		t.Fatalf("update plan: %v", err)
	}
	if updated.Name != "premium-plus" || updated.PriceCents != 2999 || updated.ValidityDays != 60 || updated.ForSale {
		t.Fatalf("updated fields mismatch: %+v", updated)
	}
	if updated.MonthlyCapUSD == nil || !updated.MonthlyCapUSD.Equal(*dec("25")) {
		t.Fatalf("monthly cap=%v, want 25", updated.MonthlyCapUSD)
	}
	// 让其变红的变异:更新 plan 行但漏掉 plan 审计的插入。
	if n := f.countInt(`SELECT count(*) FROM subscription_plan_audit_events
		WHERE tenant_id=$1 AND plan_id=$2 AND event_type=$3`,
		f.tenantA, plan.ID, AuditSubscriptionPlanUpdated); n != 1 {
		t.Fatalf("subscription_plan_updated audit count=%d, want 1", n)
	}
	if _, err := svc.UpdatePlan(ctx, UpdatePlanInput{
		TenantID: f.tenantA, PlanID: plan.ID, Name: "bad", PriceCents: -1,
		ValidityDays: 60, GrantedGroup: "premium", MonthlyCapUSD: dec("25"), ForSale: true,
		ActorAdminID: 7,
	}); !errors.Is(err, ErrPlanInvalid) {
		t.Fatalf("negative price err=%v, want ErrPlanInvalid", err)
	}
}
