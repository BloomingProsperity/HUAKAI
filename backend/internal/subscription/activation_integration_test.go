// HUAKAI · iKun
//go:build integration_pg

package subscription

import (
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// seedPlanIn 在指定租户建一个套餐 (caps nil=该窗口无限)。
func (f *subFixture) seedPlanIn(tenantID int64, name, group string, validityDays int, daily, weekly, monthly *decimal.Decimal) Plan {
	f.t.Helper()
	store := NewPostgresStore(f.pool)
	p, err := store.CreatePlan(f.ctx, createPlanRecord{
		TenantID: tenantID, Name: name, CurrencyCode: "USD", ValidityDays: validityDays,
		GrantedGroup: group, DailyCapUSD: daily, WeeklyCapUSD: weekly, MonthlyCapUSD: monthly,
		ForSale: true, Now: time.Now().UTC(),
	})
	if err != nil {
		f.t.Fatalf("seed plan %s: %v", name, err)
	}
	return p
}

// runActivate 在一条 SERIALIZABLE 事务里跑 ActivateOrRenewTx + (成功则) 写 effect, 然后提交。
// 失败 (含 ErrDowngradeNotAllowed) → 事务回滚, 模拟调用方"扣款未开通整单回滚"。
func (f *subFixture) runActivate(in ActivateInput, orderID, voucherID *int64) (ActivateResult, error) {
	f.t.Helper()
	tx, err := f.pool.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		f.t.Fatalf("begin tx: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(f.ctx)
		}
	}()
	res, err := ActivateOrRenewTx(f.ctx, tx, in)
	if err != nil {
		return res, err
	}
	eff := FulfillmentEffect{
		TenantID: in.TenantID, SourceKind: in.SourceKind, PaymentOrderID: orderID,
		VoucherRedemptionID: voucherID, UserID: in.UserID, PlanID: in.PlanID,
		UserSubscriptionID: res.Subscription.ID, ResultKind: res.ResultKind,
		AppliedValidityDays: res.AppliedValidityDays, PrevExpiresAt: res.PrevExpiresAt, NewExpiresAt: res.NewExpiresAt,
	}
	if _, err := insertFulfillmentEffectTx(f.ctx, tx, eff); err != nil {
		return res, err
	}
	if err := tx.Commit(f.ctx); err != nil {
		f.t.Fatalf("commit: %v", err)
	}
	committed = true
	return res, nil
}

func adminInput(tenantID, userID, planID int64, now time.Time, upgradeOnly bool) ActivateInput {
	return ActivateInput{
		TenantID: tenantID, UserID: userID, PlanID: planID, SourceKind: EffectSourceAdmin,
		ActorKind: ActorKindAdmin, ActorID: 1, EnforceUpgradeOnly: upgradeOnly, Now: now,
	}
}

func (f *subFixture) activeDayCapCount(tenantID, userID int64, limit string) int64 {
	return f.countInt(`SELECT count(*) FROM quota_policies
WHERE tenant_id=$1 AND scope_id=$2 AND metric='cost_usd' AND window_kind='calendar_day'
  AND enabled=true AND limit_value=$3`, tenantID, strconv.FormatInt(userID, 10), limit)
}

// activeDayCapPolicyID 返回某订阅当前 active 的日历日 cap 策略 policy_id(无则 fail)。
func (f *subFixture) activeDayCapPolicyID(tenantID, subID int64) int64 {
	f.t.Helper()
	var pid int64
	if err := f.pool.QueryRow(f.ctx, `
SELECT quota_policy_id FROM subscription_policy_links
WHERE tenant_id=$1 AND user_subscription_id=$2 AND window_kind='calendar_day' AND status='active'`,
		tenantID, subID).Scan(&pid); err != nil {
		f.t.Fatalf("read active day cap policy id: %v", err)
	}
	return pid
}

// TestPG_ActivateNew 无同组 active → 新建订阅 + 装日历日 cap 策略 + 效果行 created。
// 判别: 漏 installCapsTx → 日 cap 策略不存在 → activeDayCapCount=0 变红。
func TestPG_ActivateNew(t *testing.T) {
	f := newSubFixture(t, t.Context(), openIntegrationPool(t, t.Context()))
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	plan := f.seedPlanIn(f.tenantA, "Pro", "premium", 30, dec("10"), nil, nil)

	res, err := f.runActivate(adminInput(f.tenantA, f.userA, plan.ID, now, false), nil, nil)
	if err != nil {
		t.Fatalf("activate new: %v", err)
	}
	if res.ResultKind != ResultCreated || res.PrevExpiresAt != nil || res.AppliedValidityDays != 30 {
		t.Fatalf("result = %+v, want created/nil-prev/30d", res)
	}
	if n := f.countInt(`SELECT count(*) FROM user_subscriptions WHERE tenant_id=$1 AND user_id=$2 AND status='active'`, f.tenantA, f.userA); n != 1 {
		t.Fatalf("active subs = %d, want 1", n)
	}
	if n := f.activeDayCapCount(f.tenantA, f.userA, "10"); n != 1 {
		t.Fatalf("active daily cap=10 policies = %d, want 1", n)
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_fulfillment_effects WHERE tenant_id=$1 AND result_kind='created'`, f.tenantA); n != 1 {
		t.Fatalf("created effect rows = %d, want 1", n)
	}
	if g := f.userGroup(f.tenantA, f.userA); g != "premium" {
		t.Fatalf("user group = %q, want premium", g)
	}
}

// TestPG_RenewExtendsNotNoop 同组活跃再激活 → 到期日 max(now,现到期)+validity 严格延长 (synthesis §2B 偷钱风险)。
// 判别: 续期误走 no-op (不 UPDATE expires) → new expires 不变 → 断言 > 原到期变红。
func TestPG_RenewExtendsNotNoop(t *testing.T) {
	f := newSubFixture(t, t.Context(), openIntegrationPool(t, t.Context()))
	t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	plan := f.seedPlanIn(f.tenantA, "Pro", "premium", 30, dec("10"), nil, nil)

	r1, err := f.runActivate(adminInput(f.tenantA, f.userA, plan.ID, t0, true), nil, nil)
	if err != nil {
		t.Fatalf("first activate: %v", err)
	}
	firstExpires := r1.NewExpiresAt // t0+30d

	// 5 天后再买 (仍活跃) → 从原到期日累加。
	t1 := t0.AddDate(0, 0, 5)
	r2, err := f.runActivate(adminInput(f.tenantA, f.userA, plan.ID, t1, true), nil, nil)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if r2.ResultKind != ResultRenewed {
		t.Fatalf("result kind = %q, want renewed", r2.ResultKind)
	}
	if !r2.NewExpiresAt.After(firstExpires) {
		t.Fatalf("renewed expires %v not after first %v (no-op leak)", r2.NewExpiresAt, firstExpires)
	}
	wantExpires := firstExpires.AddDate(0, 0, 30) // 从原到期累加, 非从 now
	if !r2.NewExpiresAt.Equal(wantExpires.UTC()) {
		t.Fatalf("renewed expires = %v, want %v (accumulate from prev expiry)", r2.NewExpiresAt, wantExpires.UTC())
	}
	if r2.PrevExpiresAt == nil || !r2.PrevExpiresAt.Equal(firstExpires) {
		t.Fatalf("prev expires = %v, want %v", r2.PrevExpiresAt, firstExpires)
	}
	if n := f.countInt(`SELECT count(*) FROM user_subscriptions WHERE tenant_id=$1 AND user_id=$2 AND status='active'`, f.tenantA, f.userA); n != 1 {
		t.Fatalf("active subs after renew = %d, want 1 (still single)", n)
	}
}

// TestPG_SelfServiceDowngradeBlocked 自助买低档 (EnforceUpgradeOnly) → ErrDowngradeNotAllowed + 整事务回滚零副作用。
// 判别: 删 capsDominate 闸 → 降档生效 daily cap 变 10 → 断言仍=100 变红。
func TestPG_SelfServiceDowngradeBlocked(t *testing.T) {
	f := newSubFixture(t, t.Context(), openIntegrationPool(t, t.Context()))
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	high := f.seedPlanIn(f.tenantA, "High", "premium", 30, dec("100"), nil, nil)
	low := f.seedPlanIn(f.tenantA, "Low", "premium", 30, dec("10"), nil, nil)

	if _, err := f.runActivate(adminInput(f.tenantA, f.userA, high.ID, now, true), nil, nil); err != nil {
		t.Fatalf("activate high: %v", err)
	}
	// 自助买低档 → 拒。
	_, err := f.runActivate(adminInput(f.tenantA, f.userA, low.ID, now.AddDate(0, 0, 1), true), nil, nil)
	if err != ErrDowngradeNotAllowed {
		t.Fatalf("downgrade err = %v, want ErrDowngradeNotAllowed", err)
	}
	// 零副作用: 仍是高档 cap, 无低档生效, effect 只有最初 created 一条。
	if n := f.activeDayCapCount(f.tenantA, f.userA, "100"); n != 1 {
		t.Fatalf("daily cap=100 policies = %d, want 1 (unchanged)", n)
	}
	if n := f.activeDayCapCount(f.tenantA, f.userA, "10"); n != 0 {
		t.Fatalf("daily cap=10 policies = %d, want 0 (downgrade rejected)", n)
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_fulfillment_effects WHERE tenant_id=$1`, f.tenantA); n != 1 {
		t.Fatalf("effect rows = %d, want 1 (rejected activation wrote none)", n)
	}
}

// TestPG_SelfServiceDowngradeAllowedWhenExpired 旧高档已过期(worker 未清扫)→ 自助买低档应放行。
// only-up 闸只保护"当前持有"的更高权益; 过期=无权益, 用户买任意档都允许, 走续期从 now 起算。
// 判别: 去掉 !existing.IsExpiredAt(now) 守卫 → 闸拿失效旧 caps 挡 → 低档买被拒 → 断言成功+cap=10 变红。
func TestPG_SelfServiceDowngradeAllowedWhenExpired(t *testing.T) {
	f := newSubFixture(t, t.Context(), openIntegrationPool(t, t.Context()))
	t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	high := f.seedPlanIn(f.tenantA, "High", "premium", 30, dec("100"), nil, nil)
	low := f.seedPlanIn(f.tenantA, "Low", "premium", 30, dec("10"), nil, nil)

	if _, err := f.runActivate(adminInput(f.tenantA, f.userA, high.ID, t0, true), nil, nil); err != nil {
		t.Fatalf("activate high: %v", err)
	}
	// 高档到期日 = t0+30; 在 t0+40(已过期, worker 未跑)自助买低档。
	t1 := t0.AddDate(0, 0, 40)
	r, err := f.runActivate(adminInput(f.tenantA, f.userA, low.ID, t1, true), nil, nil)
	if err != nil {
		t.Fatalf("expired downgrade should be allowed, got %v", err)
	}
	if r.ResultKind != ResultRenewed {
		t.Fatalf("result kind = %q, want renewed", r.ResultKind)
	}
	// 新窗口从 now 起算 (旧到期已过, base=max(now,旧到期)=now), 非从旧到期累加。
	wantExpires := t1.AddDate(0, 0, 30).UTC()
	if !r.NewExpiresAt.Equal(wantExpires) {
		t.Fatalf("new expires = %v, want %v (fresh window from now)", r.NewExpiresAt, wantExpires)
	}
	if n := f.activeDayCapCount(f.tenantA, f.userA, "10"); n != 1 {
		t.Fatalf("active daily cap=10 = %d, want 1 (low plan now in effect)", n)
	}
	if n := f.activeDayCapCount(f.tenantA, f.userA, "100"); n != 0 {
		t.Fatalf("active daily cap=100 = %d, want 0 (old high cap closed)", n)
	}
}

// TestPG_UpgradeReplacesCaps 自助买高档 (处处≥) → 续期且 caps 真升级, 旧策略关新策略开。
// 判别: 续期漏覆盖 caps → active 策略仍=10 → 断言 active cap=100 变红。
// TestPG_MidCycleUpgradeReconcilesCapsInPlace 期中(未过期)升档:caps 策略应**原地 UPDATE**
// (同一 policy_id,limit 升到新值,仍 enabled),而非关旧装新铸新 policy_id。这样 quota_windows
// 按 policy_id 记的已用量在升档后完整保留(对齐 sub2api 期中续期不重置用量)。
// 判别:把 reconcileCapsTx 退回 closeCapsTx+installCapsTx → daily policy_id 变化 + 出现一条 disabled
// limit=10 的旧策略 → 下方"policy_id 不变"与"无 disabled 旧策略"两个断言转红。
func TestPG_MidCycleUpgradeReconcilesCapsInPlace(t *testing.T) {
	f := newSubFixture(t, t.Context(), openIntegrationPool(t, t.Context()))
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	low := f.seedPlanIn(f.tenantA, "Low", "premium", 30, dec("10"), nil, nil)
	high := f.seedPlanIn(f.tenantA, "High", "premium", 30, dec("100"), nil, nil)

	res, err := f.runActivate(adminInput(f.tenantA, f.userA, low.ID, now, true), nil, nil)
	if err != nil {
		t.Fatalf("activate low: %v", err)
	}
	subID := res.Subscription.ID
	pidBefore := f.activeDayCapPolicyID(f.tenantA, subID)

	if _, err := f.runActivate(adminInput(f.tenantA, f.userA, high.ID, now.AddDate(0, 0, 1), true), nil, nil); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if n := f.activeDayCapCount(f.tenantA, f.userA, "100"); n != 1 {
		t.Fatalf("active daily cap=100 = %d, want 1 (upgraded in place)", n)
	}
	// policy_id 不变 = 原地 UPDATE(若退回关旧装新会换新 id)。
	if pidAfter := f.activeDayCapPolicyID(f.tenantA, subID); pidAfter != pidBefore {
		t.Fatalf("期中升档应原地更新同一 policy_id,before=%d after=%d(换 id=用量被重置)", pidBefore, pidAfter)
	}
	// 不应出现 disabled 的旧 limit=10 策略(原地更新不留旧残行)。
	if n := f.countInt(`SELECT count(*) FROM quota_policies WHERE tenant_id=$1 AND scope_id=$2 AND window_kind='calendar_day' AND enabled=false`, f.tenantA, strconv.FormatInt(f.userA, 10)); n != 0 {
		t.Fatalf("期中升档不应留 disabled 旧日 cap 策略, 实际 %d", n)
	}
	if n := f.countInt(`SELECT count(*) FROM user_subscriptions WHERE tenant_id=$1 AND user_id=$2 AND status='active' AND daily_cap_usd=$3`, f.tenantA, f.userA, "100"); n != 1 {
		t.Fatalf("active sub with daily_cap=100 = %d, want 1 (snapshot caps updated)", n)
	}
}

// TestPG_MidCycleRepurchasePreservesUsageWindow 是 S2 护栏绕过 bug 的核心判别:自然月内复购同档
// 套餐(期中、未过期)绝不能重置已用量。构造一条带 settled_value 的 quota_windows(模拟当期已用 $7),
// 复购同档后断言:① 日 cap 策略仍是同一 policy_id;② 该 policy 的 quota_windows 已用计数仍为 7(未归零)。
// 判别:把 reconcileCapsTx 退回 closeCapsTx+installCapsTx → 铸新 policy_id → 新窗口已用为 0、旧窗口
// 被孤立 → 两个断言转红(这正是"复购清零护栏白吃成本"的资损路径)。
func TestPG_MidCycleRepurchasePreservesUsageWindow(t *testing.T) {
	f := newSubFixture(t, t.Context(), openIntegrationPool(t, t.Context()))
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	plan := f.seedPlanIn(f.tenantA, "Mid", "premium", 30, dec("50"), nil, nil)

	res, err := f.runActivate(adminInput(f.tenantA, f.userA, plan.ID, now, true), nil, nil)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	subID := res.Subscription.ID
	pid := f.activeDayCapPolicyID(f.tenantA, subID)

	// 模拟当期已用 $7:给该 policy 写一条日历日窗口的 quota_windows。
	winStart := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if _, err := f.pool.Exec(f.ctx, `
INSERT INTO quota_windows (tenant_id, policy_id, window_start, window_end, settled_value, reserved_value)
VALUES ($1, $2, $3, $4, 7, 0)`, f.tenantA, pid, winStart, winStart.AddDate(0, 0, 1)); err != nil {
		t.Fatalf("seed quota_window: %v", err)
	}

	// 期中复购同档套餐(同 plan,未过期)。
	if _, err := f.runActivate(adminInput(f.tenantA, f.userA, plan.ID, now.AddDate(0, 0, 1), true), nil, nil); err != nil {
		t.Fatalf("repurchase: %v", err)
	}

	// ① 日 cap 策略仍是同一 policy_id(未铸新)。
	if pidAfter := f.activeDayCapPolicyID(f.tenantA, subID); pidAfter != pid {
		t.Fatalf("期中复购应保留同一 policy_id(否则用量被重置), before=%d after=%d", pid, pidAfter)
	}
	// ② 该 policy 的已用量仍为 7(未归零)。
	if got := f.countInt(`SELECT count(*) FROM quota_windows WHERE tenant_id=$1 AND policy_id=$2 AND settled_value=7`, f.tenantA, pid); got != 1 {
		t.Fatalf("期中复购后已用量应保留 settled_value=7(护栏不被清零), 命中 %d 行", got)
	}
}

// TestPG_AdminOverrideDowngrade 管理员 (EnforceUpgradeOnly=false) 可降档。
// 判别: only-up 闸误用到 admin → 降档被拒 → 断言成功且 cap=10 变红。
func TestPG_AdminOverrideDowngrade(t *testing.T) {
	f := newSubFixture(t, t.Context(), openIntegrationPool(t, t.Context()))
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	high := f.seedPlanIn(f.tenantA, "High", "premium", 30, dec("100"), nil, nil)
	low := f.seedPlanIn(f.tenantA, "Low", "premium", 30, dec("10"), nil, nil)

	if _, err := f.runActivate(adminInput(f.tenantA, f.userA, high.ID, now, false), nil, nil); err != nil {
		t.Fatalf("activate high: %v", err)
	}
	if _, err := f.runActivate(adminInput(f.tenantA, f.userA, low.ID, now.AddDate(0, 0, 1), false), nil, nil); err != nil {
		t.Fatalf("admin downgrade should succeed: %v", err)
	}
	if n := f.activeDayCapCount(f.tenantA, f.userA, "10"); n != 1 {
		t.Fatalf("active daily cap=10 = %d, want 1 (admin override downgrade)", n)
	}
}

// TestPG_EffectOrderIdempotency 同订单第二次写 effect → 部分唯一索引拒 (幂等锚)。
func TestPG_EffectOrderIdempotency(t *testing.T) {
	ctx := t.Context()
	f := newSubFixture(t, ctx, openIntegrationPool(t, ctx))
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	plan := f.seedPlanIn(f.tenantA, "Pro", "premium", 30, dec("10"), nil, nil)
	res, err := f.runActivate(adminInput(f.tenantA, f.userA, plan.ID, now, false), nil, nil)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	// 建一笔订阅订单。
	var orderID int64
	if err := f.pool.QueryRow(ctx, `
INSERT INTO payment_orders (tenant_id, user_id, out_trade_no, amount_cents, order_kind, subscription_plan_id, status)
VALUES ($1,$2,$3,$4,'subscription',$5,'recharging') RETURNING id`,
		f.tenantA, f.userA, "sub-"+f.suffix, 999, plan.ID).Scan(&orderID); err != nil {
		t.Fatalf("insert order: %v", err)
	}
	eff := FulfillmentEffect{
		TenantID: f.tenantA, SourceKind: EffectSourceOrder, PaymentOrderID: &orderID,
		UserID: f.userA, PlanID: plan.ID, UserSubscriptionID: res.Subscription.ID,
		ResultKind: ResultCreated, AppliedValidityDays: 30, NewExpiresAt: res.NewExpiresAt,
	}
	insertEffect := func() error {
		tx, _ := f.pool.BeginTx(ctx, pgx.TxOptions{})
		defer tx.Rollback(ctx)
		if _, err := insertFulfillmentEffectTx(ctx, tx, eff); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if err := insertEffect(); err != nil {
		t.Fatalf("first effect insert: %v", err)
	}
	if err := insertEffect(); err == nil {
		t.Fatalf("second effect insert for same order should violate unique index")
	}
	// 反查能命中。
	tx, _ := f.pool.BeginTx(ctx, pgx.TxOptions{})
	defer tx.Rollback(ctx)
	got, ok, err := getFulfillmentEffectByOrderTx(ctx, tx, f.tenantA, orderID)
	if err != nil || !ok || got.PaymentOrderID == nil || *got.PaymentOrderID != orderID {
		t.Fatalf("get effect by order: got=%+v ok=%v err=%v", got, ok, err)
	}
}

// TestPG_ActivationRollback 调用方回滚 → 激活零落地 (扣款未开通=整单回滚)。
func TestPG_ActivationRollback(t *testing.T) {
	ctx := t.Context()
	f := newSubFixture(t, ctx, openIntegrationPool(t, ctx))
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	plan := f.seedPlanIn(f.tenantA, "Pro", "premium", 30, dec("10"), nil, nil)

	tx, err := f.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := ActivateOrRenewTx(ctx, tx, adminInput(f.tenantA, f.userA, plan.ID, now, false)); err != nil {
		t.Fatalf("activate in tx: %v", err)
	}
	_ = tx.Rollback(ctx) // 模拟调用方后续步骤失败 → 整事务回滚

	if n := f.countInt(`SELECT count(*) FROM user_subscriptions WHERE tenant_id=$1 AND user_id=$2`, f.tenantA, f.userA); n != 0 {
		t.Fatalf("subs after rollback = %d, want 0", n)
	}
	if n := f.countInt(`SELECT count(*) FROM quota_policies WHERE tenant_id=$1 AND scope_id=$2`, f.tenantA, strconv.FormatInt(f.userA, 10)); n != 0 {
		t.Fatalf("policies after rollback = %d, want 0", n)
	}
	if g := f.userGroup(f.tenantA, f.userA); g != "default" {
		t.Fatalf("user group after rollback = %q, want default", g)
	}
}

// TestPG_ActivationCrossTenant 激活 A 不触 B (同 user_id 数值跨租户)。
// 判别: ActivateOrRenewTx 某查询漏 tenant 谓词 → 串到 B → 断言 B 订阅数不变变红。
func TestPG_ActivationCrossTenant(t *testing.T) {
	f := newSubFixture(t, t.Context(), openIntegrationPool(t, t.Context()))
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	planA := f.seedPlanIn(f.tenantA, "Pro", "premium", 30, dec("10"), nil, nil)
	planB := f.seedPlanIn(f.tenantB, "Pro", "premium", 30, dec("10"), nil, nil)

	// B 先激活一条。
	if _, err := f.runActivate(adminInput(f.tenantB, f.userB, planB.ID, now, false), nil, nil); err != nil {
		t.Fatalf("activate B: %v", err)
	}
	bExpiresBefore := f.userSubExpires(f.tenantB, f.userB)
	// A 激活。
	if _, err := f.runActivate(adminInput(f.tenantA, f.userA, planA.ID, now, false), nil, nil); err != nil {
		t.Fatalf("activate A: %v", err)
	}
	if n := f.countInt(`SELECT count(*) FROM user_subscriptions WHERE tenant_id=$1`, f.tenantB); n != 1 {
		t.Fatalf("tenant B subs = %d, want 1 (A activation must not leak)", n)
	}
	if got := f.userSubExpires(f.tenantB, f.userB); !got.Equal(bExpiresBefore) {
		t.Fatalf("tenant B expires changed by A activation: %v -> %v", bExpiresBefore, got)
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_fulfillment_effects WHERE tenant_id=$1`, f.tenantB); n != 1 {
		t.Fatalf("tenant B effect rows = %d, want 1", n)
	}
}

// TestPG_ZeroTouchMoneyTables 订阅激活零碰 billing_events / payment_credits (红线)。
// 判别: 激活分支误写钱表 → COUNT 变化变红。
func TestPG_ZeroTouchMoneyTables(t *testing.T) {
	f := newSubFixture(t, t.Context(), openIntegrationPool(t, t.Context()))
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	plan := f.seedPlanIn(f.tenantA, "Pro", "premium", 30, dec("10"), nil, nil)

	beBefore := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1`, f.tenantA)
	pcBefore := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1`, f.tenantA)
	if _, err := f.runActivate(adminInput(f.tenantA, f.userA, plan.ID, now, false), nil, nil); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if got := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1`, f.tenantA); got != beBefore {
		t.Fatalf("billing_events count changed %d -> %d (subscription must not touch money ledger)", beBefore, got)
	}
	if got := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1`, f.tenantA); got != pcBefore {
		t.Fatalf("payment_credits count changed %d -> %d", pcBefore, got)
	}
}
