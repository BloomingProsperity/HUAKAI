// HUAKAI · iKun
//go:build integration_pg

// 订阅 P3a 真 PG 判别测试。每个测试守一个具体权益缺陷, fixture 设计成 mutation 即变红:
//   S1 重复授予不双权益 (幂等 + 不重复装策略)
//   S2 授予升级用户分组 + 记录购买前组
//   S3 到期降级 + 关配额策略 + 关 link (经 worker 扫描驱动)
//   S4 到期降级守卫: 有更新升级时旧订阅到期不误降
//   S5 配额上限按窗口装进 quota_policies (enforce/enabled/limit/有效期精确)
//   S6 跨租户隔离 (A 授予不动 B 的用户组/策略)
//   S7 并发授予只一条 active (无双权益)
//   S8 操作审计轨迹落库

package subscription

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

func baseTime() time.Time { return time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC) }

// createPremiumPlan 建一个授予 premium + 月上限 10 USD 的套餐。
func createPremiumPlan(t *testing.T, ctx context.Context, svc *Service, tenantID int64, group string) Plan {
	t.Helper()
	plan, err := svc.CreatePlan(ctx, CreatePlanInput{
		TenantID: tenantID, Name: "premium-" + group, ValidityDays: 30,
		GrantedGroup: group, MonthlyCapUSD: dec("10"),
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	return plan
}

// S1: 重复授予 → 第二次幂等返回同一订阅; 只有一条 active; 月策略只装一条。
func TestSubscriptionPostgres_DuplicateAssignNoDoubleEntitlement(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	plan := createPremiumPlan(t, ctx, svc, f.tenantA, "premium")

	r1, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID, ActorAdminID: 7})
	if err != nil || r1.Idempotent {
		t.Fatalf("first assign: err=%v idempotent=%v", err, r1.Idempotent)
	}
	r2, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("second assign: %v", err)
	}
	if !r2.Idempotent || r2.Subscription.ID != r1.Subscription.ID {
		t.Fatalf("duplicate assign must replay same sub: idempotent=%v id1=%d id2=%d", r2.Idempotent, r1.Subscription.ID, r2.Subscription.ID)
	}
	if n := f.countInt(`SELECT count(*) FROM user_subscriptions WHERE tenant_id=$1 AND user_id=$2 AND status='active'`, f.tenantA, f.userA); n != 1 {
		t.Fatalf("active subscription count = %d, want 1", n)
	}
	// 自证: 月策略只装一条 (mutation: 去幂等 → 双策略 → 红)。
	scope := strconv.FormatInt(f.userA, 10)
	if n := f.countInt(`SELECT count(*) FROM quota_policies WHERE tenant_id=$1 AND scope_kind='user' AND scope_id=$2 AND metric='cost_usd' AND enabled=true`, f.tenantA, scope); n != 1 {
		t.Fatalf("enabled cost_usd policy count = %d, want 1", n)
	}
}

// SUB-045: 后台按 granted_group 查询订阅必须只返回该组, 且包含全状态。
// MUTATION: 去掉 granted_group WHERE → tenant 内 3 条订阅都会返回, 本测试从 2 变 3 后变红。
func TestListSubscriptionsByGroup(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	vipPlan := createPremiumPlan(t, ctx, svc, f.tenantA, "vip")
	basicPlan := createPremiumPlan(t, ctx, svc, f.tenantA, "basic")

	if _, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: vipPlan.ID, ActorAdminID: 7}); err != nil {
		t.Fatalf("assign vip userA: %v", err)
	}
	if _, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA2, PlanID: vipPlan.ID, ActorAdminID: 7}); err != nil {
		t.Fatalf("assign vip userA2: %v", err)
	}
	if _, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: basicPlan.ID, ActorAdminID: 7}); err != nil {
		t.Fatalf("assign basic userA: %v", err)
	}

	subs, err := svc.ListUserSubscriptionsByGroup(ctx, f.tenantA, "vip", 10)
	if err != nil {
		t.Fatalf("list by group: %v", err)
	}
	if len(subs) != 2 {
		t.Fatalf("vip subscriptions len=%d want 2: %+v", len(subs), subs)
	}
	for _, sub := range subs {
		if sub.GrantedGroup != "vip" {
			t.Fatalf("returned non-vip subscription: %+v", sub)
		}
		if sub.TenantID != f.tenantA {
			t.Fatalf("returned cross-tenant subscription: %+v", sub)
		}
	}
}

// S2: 授予 → users.user_group 升到 granted_group, prev_user_group 记 default。
func TestSubscriptionPostgres_AssignUpgradesUserGroup(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	plan := createPremiumPlan(t, ctx, svc, f.tenantA, "premium")

	if g := f.userGroup(f.tenantA, f.userA); g != "default" {
		t.Fatalf("pre-assign group = %q, want default", g)
	}
	r, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	// mutation: 跳过 UPDATE users → group 仍 default → 红。
	if g := f.userGroup(f.tenantA, f.userA); g != "premium" {
		t.Fatalf("post-assign group = %q, want premium", g)
	}
	if r.Subscription.PrevUserGroup != "default" {
		t.Fatalf("prev_user_group = %q, want default", r.Subscription.PrevUserGroup)
	}
}

// S3: 到期 worker → 标 expired + 降回 default + 关策略 + 关 link。
func TestSubscriptionPostgres_ExpiryDowngradesAndClosesPolicy(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	plan := createPremiumPlan(t, ctx, svc, f.tenantA, "premium")

	r, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	// 推进到过期后, worker 扫描并到期。
	clk.set(baseTime().AddDate(0, 0, 31))
	n, err := svc.ProcessDueExpiries(ctx, 100)
	if err != nil || n != 1 {
		t.Fatalf("process due expiries: n=%d err=%v", n, err)
	}
	if got, err := svc.GetSubscription(ctx, f.tenantA, r.Subscription.ID); err != nil || got.Status != StatusExpired {
		t.Fatalf("status after expiry = %v err=%v, want expired", got.Status, err)
	}
	// mutation: 跳过降级 → 仍 premium → 红。
	if g := f.userGroup(f.tenantA, f.userA); g != "default" {
		t.Fatalf("post-expiry group = %q, want default", g)
	}
	// mutation: 跳过 closeCaps → 策略仍 enabled → 红。
	scope := strconv.FormatInt(f.userA, 10)
	if n := f.countInt(`SELECT count(*) FROM quota_policies WHERE tenant_id=$1 AND scope_id=$2 AND enabled=true`, f.tenantA, scope); n != 0 {
		t.Fatalf("enabled policy count after expiry = %d, want 0", n)
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_policy_links WHERE tenant_id=$1 AND user_subscription_id=$2 AND status='active'`, f.tenantA, r.Subscription.ID); n != 0 {
		t.Fatalf("active policy links after expiry = %d, want 0", n)
	}
}

// S4: 降级守卫 — 用户有更新的 premium2 升级, 旧 premium 到期不应把用户拉回 default。
func TestSubscriptionPostgres_ExpiryGuardSkipsWhenNewerUpgrade(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	store := NewPostgresStore(pool)
	svc := NewService(store, WithClock(clk.now))
	planPremium := createPremiumPlan(t, ctx, svc, f.tenantA, "premium")
	planPremium2 := createPremiumPlan(t, ctx, svc, f.tenantA, "premium2")

	sub1, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: planPremium.ID, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("assign premium: %v", err)
	}
	// 第二次升级到 premium2 (用户当前组变 premium2, prev=premium)。
	if _, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: planPremium2.ID, ActorAdminID: 7}); err != nil {
		t.Fatalf("assign premium2: %v", err)
	}
	if g := f.userGroup(f.tenantA, f.userA); g != "premium2" {
		t.Fatalf("after upgrade group = %q, want premium2", g)
	}
	// 推进到到期后再到期 (生产里到期只发生在到点行; 到期路径锁内复查 expires_at<=now)。
	clk.set(sub1.Subscription.ExpiresAt.Add(time.Hour))
	// 到期第一个 (premium) 订阅: 用户当前组是 premium2 != premium → 守卫不降级。
	if _, err := store.ExpireSubscription(ctx, lifecycleRecord{
		TenantID: f.tenantA, SubscriptionID: sub1.Subscription.ID, ActorKind: ActorKindSystem, Now: clk.now(),
	}); err != nil {
		t.Fatalf("expire sub1: %v", err)
	}
	// mutation: 去掉 currentGroup==granted_group 守卫 → 会降回 sub1.prev=default → 红。
	if g := f.userGroup(f.tenantA, f.userA); g != "premium2" {
		t.Fatalf("group after expiring older sub = %q, want premium2 (guard must skip downgrade)", g)
	}
}

// S4b: 链式升级 default->basic->premium, 先到期 basic 再到期 premium → 用户回 default,
// 不得留在已到期的 basic (旧 snapshot-restore 实现会错误回 basic)。
func TestSubscriptionPostgres_ChainedExpiryResolvesToDefault(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	store := NewPostgresStore(pool)
	svc := NewService(store, WithClock(clk.now))
	planBasic := createPremiumPlan(t, ctx, svc, f.tenantA, "basic")
	planPremium := createPremiumPlan(t, ctx, svc, f.tenantA, "premium")

	subBasic, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: planBasic.ID, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("assign basic: %v", err)
	}
	subPremium, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: planPremium.ID, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("assign premium: %v", err)
	}
	if g := f.userGroup(f.tenantA, f.userA); g != "premium" {
		t.Fatalf("after chain group = %q, want premium", g)
	}
	// 推进到到期后 (两订阅同 validity 均到点; 到期路径锁内复查 expires_at<=now)。
	clk.set(subPremium.Subscription.ExpiresAt.Add(time.Hour))
	// 先到期 basic: current=premium != basic → 守卫跳过, 用户仍 premium (分组解析按 status 不看 expires_at)。
	if _, err := store.ExpireSubscription(ctx, lifecycleRecord{TenantID: f.tenantA, SubscriptionID: subBasic.Subscription.ID, ActorKind: ActorKindSystem, Now: clk.now()}); err != nil {
		t.Fatalf("expire basic: %v", err)
	}
	if g := f.userGroup(f.tenantA, f.userA); g != "premium" {
		t.Fatalf("after expiring basic group = %q, want premium", g)
	}
	// 再到期 premium: 无剩余 active 订阅 → 回 default (旧实现错误回 basic → 红)。
	if _, err := store.ExpireSubscription(ctx, lifecycleRecord{TenantID: f.tenantA, SubscriptionID: subPremium.Subscription.ID, ActorKind: ActorKindSystem, Now: clk.now()}); err != nil {
		t.Fatalf("expire premium: %v", err)
	}
	if g := f.userGroup(f.tenantA, f.userA); g != "default" {
		t.Fatalf("after chained expiry group = %q, want default (must NOT restore expired basic)", g)
	}
}

// S5: 配额上限按窗口精确装进 quota_policies (enforce / enabled / 限额 / 有效期)。
func TestSubscriptionPostgres_QuotaPolicyInstalledShape(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	plan, err := svc.CreatePlan(ctx, CreatePlanInput{
		TenantID: f.tenantA, Name: "multi-cap", ValidityDays: 30, GrantedGroup: "premium",
		DailyCapUSD: dec("5"), MonthlyCapUSD: dec("10"),
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	r, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	scope := strconv.FormatInt(f.userA, 10)
	expiresAt := r.Subscription.ExpiresAt
	startsAt := r.Subscription.StartsAt
	// daily: calendar_day limit 5, enforce, enabled, valid_from=starts, valid_until=expires。
	if n := f.countInt(`SELECT count(*) FROM quota_policies
		WHERE tenant_id=$1 AND scope_kind='user' AND scope_id=$2 AND metric='cost_usd'
		AND window_kind='calendar_day' AND mode='enforce' AND enabled=true
		AND limit_value=$3::numeric AND valid_from=$4 AND valid_until=$5`,
		f.tenantA, scope, "5", startsAt, expiresAt); n != 1 {
		t.Fatalf("daily cost_usd policy shape mismatch: count=%d, want 1", n)
	}
	// monthly: calendar_month limit 10。 mutation: 装错 window/limit/valid_until → 红。
	if n := f.countInt(`SELECT count(*) FROM quota_policies
		WHERE tenant_id=$1 AND scope_kind='user' AND scope_id=$2 AND metric='cost_usd'
		AND window_kind='calendar_month' AND mode='enforce' AND enabled=true
		AND limit_value=$3::numeric AND valid_until=$4`,
		f.tenantA, scope, "10", expiresAt); n != 1 {
		t.Fatalf("monthly cost_usd policy shape mismatch: count=%d, want 1", n)
	}
}

// S6: 跨租户隔离 — A 授予不动 B 的用户组/策略。
func TestSubscriptionPostgres_CrossTenantIsolation(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	plan := createPremiumPlan(t, ctx, svc, f.tenantA, "premium")

	if _, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID, ActorAdminID: 7}); err != nil {
		t.Fatalf("assign A: %v", err)
	}
	// mutation: 漏 tenant 谓词 → B 用户被波及 → 红。
	if g := f.userGroup(f.tenantB, f.userB); g != "default" {
		t.Fatalf("tenant B user group = %q, want default (cross-tenant leak)", g)
	}
	if n := f.countInt(`SELECT count(*) FROM quota_policies WHERE tenant_id=$1`, f.tenantB); n != 0 {
		t.Fatalf("tenant B quota policy count = %d, want 0", n)
	}
	// B 引用 A 的 plan_id 应找不到 (tenant-scoped)。
	if _, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantB, UserID: f.userB, PlanID: plan.ID, ActorAdminID: 7}); err == nil {
		t.Fatalf("assigning tenant-A plan under tenant B must fail")
	}
}

// S7: 并发授予同 (user, plan) → 恰一条 active, 仅一个创建胜出, 其余幂等。
func TestSubscriptionPostgres_ConcurrentAssignSingleActive(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	plan := createPremiumPlan(t, ctx, svc, f.tenantA, "premium")

	const goroutines = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	created, idempotent, failed := 0, 0, 0
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID, ActorAdminID: 7})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				failed++
			case res.Idempotent:
				idempotent++
			default:
				created++
			}
		}()
	}
	close(start)
	wg.Wait()

	if failed != 0 {
		t.Fatalf("concurrent assign failures = %d, want 0", failed)
	}
	// mutation: 非事务/无 partial unique → 多个 created → 红。
	if created != 1 {
		t.Fatalf("created = %d, want exactly 1 (rest idempotent=%d)", created, idempotent)
	}
	if n := f.countInt(`SELECT count(*) FROM user_subscriptions WHERE tenant_id=$1 AND user_id=$2 AND status='active'`, f.tenantA, f.userA); n != 1 {
		t.Fatalf("active subscription count = %d, want 1", n)
	}
	if n := f.countInt(`SELECT count(*) FROM quota_policies WHERE tenant_id=$1 AND scope_id=$2 AND enabled=true`, f.tenantA, strconv.FormatInt(f.userA, 10)); n != 1 {
		t.Fatalf("enabled policy count = %d, want 1 (no double install)", n)
	}
}

// S8: 审计轨迹 — 授予记 subscription_created+group_upgraded; 到期记 expired+group_downgraded。
func TestSubscriptionPostgres_AuditTrail(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newSubFixture(t, ctx, pool)
	clk := &fakeClock{t: baseTime()}
	svc := NewService(NewPostgresStore(pool), WithClock(clk.now))
	plan := createPremiumPlan(t, ctx, svc, f.tenantA, "premium")

	r, err := svc.AssignSubscription(ctx, AssignSubscriptionInput{TenantID: f.tenantA, UserID: f.userA, PlanID: plan.ID, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	clk.set(baseTime().AddDate(0, 0, 31))
	if _, err := svc.ProcessDueExpiries(ctx, 100); err != nil {
		t.Fatalf("expire: %v", err)
	}
	events, err := svc.ListAuditEvents(ctx, f.tenantA, r.Subscription.ID)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	seen := map[string]bool{}
	for _, ev := range events {
		seen[ev.EventType] = true
	}
	for _, want := range []string{AuditSubscriptionCreated, AuditGroupUpgraded, AuditExpired, AuditGroupDowngraded} {
		if !seen[want] {
			t.Fatalf("missing audit event %q; got %v", want, seen)
		}
	}
}
