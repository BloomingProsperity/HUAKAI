// HUAKAI · iKun
//go:build integration_pg

package payment

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
)

// registerSubscriptionCleanup 在 payment 夹具基础上追加订阅相关表清理。
// t.Cleanup 是 LIFO: 本清理在 newPaymentFixture 之后注册 → 先于 payment 清理执行,
// 满足 subscription_fulfillment_effects(FK→payment_orders) 必须先于 orders 删除的顺序。
func registerSubscriptionCleanup(f *paymentFixture) {
	f.t.Cleanup(func() {
		ctx := context.Background()
		for _, tenantID := range []int64{f.tenantA, f.tenantB} {
			for _, q := range []string{
				`DELETE FROM subscription_fulfillment_effects WHERE tenant_id=$1`,
				`DELETE FROM subscription_audit_events WHERE tenant_id=$1`,
				`DELETE FROM subscription_policy_links WHERE tenant_id=$1`,
				`DELETE FROM user_subscriptions WHERE tenant_id=$1`,
				`DELETE FROM subscription_plans WHERE tenant_id=$1`,
				`DELETE FROM quota_policies WHERE tenant_id=$1`,
			} {
				_, _ = f.pool.Exec(ctx, q, tenantID)
			}
		}
	})
}

func seedSubPlan(f *paymentFixture, name, group string, validityDays int, daily *decimal.Decimal, priceCentsOpt ...int64) subscription.Plan {
	f.t.Helper()
	priceCents := int64(999)
	if len(priceCentsOpt) > 0 {
		priceCents = priceCentsOpt[0]
	}
	p, err := subscription.NewService(subscription.NewPostgresStore(f.pool)).CreatePlan(f.ctx, subscription.CreatePlanInput{
		TenantID: f.tenantA, Name: name, CurrencyCode: "USD", ValidityDays: validityDays,
		PriceCents: priceCents, GrantedGroup: group, DailyCapUSD: daily, ForSale: true,
	})
	if err != nil {
		f.t.Fatalf("seed plan %s: %v", name, err)
	}
	return p
}

func subDec(s string) *decimal.Decimal {
	d := decimal.RequireFromString(s)
	return &d
}

// TestPG_OrderCreate_IdempotencyRejectsProductMismatch 守订单产品语义幂等:
// 同 out_trade_no 即使金额/用户/渠道相同, order_kind 或 subscription_plan_id 不同也必须冲突。
// 判别: duplicate replay 只比金额/用户/渠道 → topup 被伪装成 subscription 或 A 套餐被伪装成 B 套餐 → 红。
func TestPG_OrderCreate_IdempotencyRejectsProductMismatch(t *testing.T) {
	ctx := context.Background()
	f := newPaymentFixture(t, ctx, openPaymentIntegrationPool(t, ctx))
	registerSubscriptionCleanup(f)
	planA := seedSubPlan(f, "PlanA", "premium", 30, subDec("10"))
	planB := seedSubPlan(f, "PlanB", "premium", 60, subDec("20"))
	svc := NewService(NewPostgresStore(f.pool))

	outTopup := "kind-mismatch-" + f.suffix
	if _, err := svc.CreateOrder(ctx, CreateOrderInput{
		TenantID: f.tenantA, UserID: f.userA, AmountCents: 999, CurrencyCode: "USD",
		OutTradeNo: outTopup, ActorAdminID: 1,
	}); err != nil {
		t.Fatalf("create topup seed: %v", err)
	}
	_, err := svc.CreateOrder(ctx, CreateOrderInput{
		TenantID: f.tenantA, UserID: f.userA, AmountCents: 999, CurrencyCode: "USD",
		OutTradeNo: outTopup, ActorAdminID: 1,
		OrderKind: OrderKindSubscription, SubscriptionPlanID: &planA.ID,
	})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("topup/subscription duplicate err=%v, want ErrIdempotencyConflict", err)
	}

	outPlan := "plan-mismatch-" + f.suffix
	first, err := svc.CreateOrder(ctx, CreateOrderInput{
		TenantID: f.tenantA, UserID: f.userA, AmountCents: 999, CurrencyCode: "USD",
		OutTradeNo: outPlan, ActorAdminID: 1,
		OrderKind: OrderKindSubscription, SubscriptionPlanID: &planA.ID,
	})
	if err != nil {
		t.Fatalf("create plan A seed: %v", err)
	}
	replay, err := svc.CreateOrder(ctx, CreateOrderInput{
		TenantID: f.tenantA, UserID: f.userA, AmountCents: 999, CurrencyCode: "USD",
		OutTradeNo: outPlan, ActorAdminID: 1,
		OrderKind: OrderKindSubscription, SubscriptionPlanID: &planA.ID,
	})
	if err != nil || !replay.Idempotent || replay.Order.ID != first.Order.ID {
		t.Fatalf("same plan replay err=%v idempotent=%v id=%d want idempotent original %d", err, replay.Idempotent, replay.Order.ID, first.Order.ID)
	}
	_, err = svc.CreateOrder(ctx, CreateOrderInput{
		TenantID: f.tenantA, UserID: f.userA, AmountCents: 999, CurrencyCode: "USD",
		OutTradeNo: outPlan, ActorAdminID: 1,
		OrderKind: OrderKindSubscription, SubscriptionPlanID: &planB.ID,
	})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("plan A/B duplicate err=%v, want ErrIdempotencyConflict", err)
	}
}

func (f *paymentFixture) dayCapCount(userID int64, limit string) int64 {
	return f.countInt(`SELECT count(*) FROM quota_policies
WHERE tenant_id=$1 AND scope_id=$2 AND metric='cost_usd' AND window_kind='calendar_day'
  AND enabled=true AND limit_value=$3`, f.tenantA, strconv.FormatInt(userID, 10), limit)
}

// createSubOrder 建一笔订阅单 (manual provider, pending); amountCents 是 caller 报价,
// 真正落单金额应由 service 从 plan price 派生。
func (f *paymentFixture) createSubOrder(svc *Service, planID int64, outTradeNo string, amountCents int64) Order {
	f.t.Helper()
	res, err := svc.CreateOrder(f.ctx, CreateOrderInput{
		TenantID: f.tenantA, UserID: f.userA, AmountCents: amountCents, CurrencyCode: "USD",
		OutTradeNo: outTradeNo, ActorAdminID: 1,
		OrderKind: OrderKindSubscription, SubscriptionPlanID: &planID,
	})
	if err != nil {
		f.t.Fatalf("create subscription order %s: %v", outTradeNo, err)
	}
	return res.Order
}

// TestPG_OrderSubscription_ActivatesZeroTouch 订阅单 confirm+fulfill → 订阅激活 + 装日 cap + effect(order 源),
// 且零碰钱表 (billing_events 不增 / payment_credits=0 / 支付余额=0); 订单进 completed。
// 判别: completeFulfillOnce 漏 order_kind 分支走 credit 路径 → 写 payment_credited → billing_events 增量 → 红 (掺水反向)。
func TestPG_OrderSubscription_ActivatesZeroTouch(t *testing.T) {
	ctx := context.Background()
	f := newPaymentFixture(t, ctx, openPaymentIntegrationPool(t, ctx))
	registerSubscriptionCleanup(f)
	plan := seedSubPlan(f, "Pro", "premium", 30, subDec("10"), 4999)
	svc := NewService(NewPostgresStore(f.pool))

	beBefore := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1`, f.tenantA)
	order := f.createSubOrder(svc, plan.ID, "sub-order-"+f.suffix, 99)
	if order.AmountCents != 4999 || order.CurrencyCode != "USD" {
		t.Fatalf("subscription order money = %d %s, want plan price snapshot 4999 USD", order.AmountCents, order.CurrencyCode)
	}
	res, err := svc.AdminConfirmPaid(ctx, AdminConfirmPaidInput{TenantID: f.tenantA, OrderID: order.ID, ActorAdminID: 1})
	if err != nil {
		t.Fatalf("confirm+fulfill subscription order: %v", err)
	}
	if res.Subscription == nil {
		t.Fatalf("expected subscription grant, got nil (credit path taken?)")
	}
	if res.Subscription.ResultKind != subscription.ResultCreated {
		t.Fatalf("result kind = %q, want created", res.Subscription.ResultKind)
	}
	if res.Order.Status != StatusCompleted {
		t.Fatalf("order status = %q, want completed", res.Order.Status)
	}
	if n := f.countInt(`SELECT count(*) FROM user_subscriptions WHERE tenant_id=$1 AND user_id=$2 AND status='active'`, f.tenantA, f.userA); n != 1 {
		t.Fatalf("active subs = %d, want 1", n)
	}
	if n := f.dayCapCount(f.userA, "10"); n != 1 {
		t.Fatalf("daily cap=10 policies = %d, want 1", n)
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_fulfillment_effects WHERE tenant_id=$1 AND source_kind='order' AND payment_order_id=$2`, f.tenantA, order.ID); n != 1 {
		t.Fatalf("order effect rows = %d, want 1", n)
	}
	// 红线: 订阅单零碰钱表。
	if got := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1`, f.tenantA); got != beBefore {
		t.Fatalf("billing_events changed %d -> %d (subscription order must not write money ledger)", beBefore, got)
	}
	if got := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1`, f.tenantA); got != 0 {
		t.Fatalf("payment_credits = %d, want 0", got)
	}
	bal, err := svc.GetBalance(ctx, f.tenantA, f.userA)
	if err != nil {
		t.Fatalf("get balance: %v", err)
	}
	if bal.AmountCents != 0 {
		t.Fatalf("payment balance = %d, want 0 (subscription order writes no credit)", bal.AmountCents)
	}
}

// TestPG_OrderSubscription_IdempotentReplay 已完成订阅单二次 fulfill → 订阅只延一次 + effect 仅一行。
// 判别: 完成态重放对订阅单误读 credit(不存在)报错 / 漏 effect 幂等 → 到期跳 → 红。
func TestPG_OrderSubscription_IdempotentReplay(t *testing.T) {
	ctx := context.Background()
	f := newPaymentFixture(t, ctx, openPaymentIntegrationPool(t, ctx))
	registerSubscriptionCleanup(f)
	plan := seedSubPlan(f, "Pro", "premium", 30, subDec("10"))
	svc := NewService(NewPostgresStore(f.pool))

	order := f.createSubOrder(svc, plan.ID, "sub-order-"+f.suffix, 999)
	r1, err := svc.AdminConfirmPaid(ctx, AdminConfirmPaidInput{TenantID: f.tenantA, OrderID: order.ID, ActorAdminID: 1})
	if err != nil {
		t.Fatalf("first confirm+fulfill: %v", err)
	}
	exp1 := r1.Subscription.NewExpiresAt

	// 已 completed, 再 fulfill 一次 → 完成态幂等重放。
	r2, err := svc.Fulfill(ctx, FulfillInput{TenantID: f.tenantA, OrderID: order.ID, ActorKind: "admin", ActorID: 1})
	if err != nil {
		t.Fatalf("replay fulfill: %v", err)
	}
	if !r2.Idempotent {
		t.Fatalf("replay should be idempotent")
	}
	if r2.Subscription == nil || !r2.Subscription.NewExpiresAt.Equal(exp1) {
		t.Fatalf("replay expires = %+v, want unchanged %v", r2.Subscription, exp1)
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_fulfillment_effects WHERE tenant_id=$1`, f.tenantA); n != 1 {
		t.Fatalf("effect rows = %d, want 1 (replay must not double-fulfill)", n)
	}
	if n := f.countInt(`SELECT count(*) FROM user_subscriptions WHERE tenant_id=$1 AND user_id=$2 AND status='active'`, f.tenantA, f.userA); n != 1 {
		t.Fatalf("active subs = %d, want 1", n)
	}
}

// TestPG_OrderSubscription_DowngradeBlocked 已持高档活跃, 低档订阅单 fulfill → ErrDowngradeNotAllowed + 整事务回滚 (单留 recharging)。
// 判别: FulfillOrderTx EnforceUpgradeOnly=false → 降档生效 → 低档 cap 出现 → 红。
func TestPG_OrderSubscription_DowngradeBlocked(t *testing.T) {
	ctx := context.Background()
	f := newPaymentFixture(t, ctx, openPaymentIntegrationPool(t, ctx))
	registerSubscriptionCleanup(f)
	high := seedSubPlan(f, "High", "premium", 30, subDec("100"))
	low := seedSubPlan(f, "Low", "premium", 30, subDec("10"))
	svc := NewService(NewPostgresStore(f.pool))

	highOrder := f.createSubOrder(svc, high.ID, "high-"+f.suffix, 9999)
	if _, err := svc.AdminConfirmPaid(ctx, AdminConfirmPaidInput{TenantID: f.tenantA, OrderID: highOrder.ID, ActorAdminID: 1}); err != nil {
		t.Fatalf("activate high: %v", err)
	}

	lowOrder := f.createSubOrder(svc, low.ID, "low-"+f.suffix, 999)
	_, err := svc.AdminConfirmPaid(ctx, AdminConfirmPaidInput{TenantID: f.tenantA, OrderID: lowOrder.ID, ActorAdminID: 1})
	if !errors.Is(err, subscription.ErrDowngradeNotAllowed) || !errors.Is(err, ErrOrderFulfillFailed) {
		t.Fatalf("downgrade err = %v, want ErrOrderFulfillFailed 包裹 ErrDowngradeNotAllowed", err)
	}
	// 确定性不可履约 → 订单转终态 failed(此前留 recharging = 已付款订单永久悬空:
	// webhook 重投/admin 重点都撞同一堵墙, 且无清扫器认领 recharging)。
	// mutation: completeFulfillOnce 去掉 isDeterministicFulfillFailure 分支退回
	// `return FulfillResult{}, err` → status 停 recharging + 无审计行 → 双断言红。
	got, err := svc.GetOrder(ctx, f.tenantA, lowOrder.ID)
	if err != nil {
		t.Fatalf("get low order: %v", err)
	}
	if got.Status != StatusFailed {
		t.Fatalf("low order status = %q, want failed (确定性失败必须终态化, 否则订单悬空)", got.Status)
	}
	if n := f.countInt(`SELECT count(*) FROM payment_audit_events WHERE tenant_id=$1 AND payment_order_id=$2 AND event_type='fulfillment_failed'`, f.tenantA, lowOrder.ID); n != 1 {
		t.Fatalf("fulfillment_failed 审计行 = %d, want 1 (终态化必须留痕供 admin 处置退款)", n)
	}
	// 幂等: 对 failed 单重投确认 → 不可确认冲突, 不复活不重试。
	if _, rerr := svc.AdminConfirmPaid(ctx, AdminConfirmPaidInput{TenantID: f.tenantA, OrderID: lowOrder.ID, ActorAdminID: 1}); !errors.Is(rerr, ErrOrderNotConfirmable) {
		t.Fatalf("failed 单重投 err = %v, want ErrOrderNotConfirmable", rerr)
	}
	// 低档无生效, 仍是高档 cap; effect 只有高档那条。
	if n := f.dayCapCount(f.userA, "10"); n != 0 {
		t.Fatalf("low cap=10 policies = %d, want 0 (downgrade rejected)", n)
	}
	if n := f.dayCapCount(f.userA, "100"); n != 1 {
		t.Fatalf("high cap=100 policies = %d, want 1 (unchanged)", n)
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_fulfillment_effects WHERE tenant_id=$1`, f.tenantA); n != 1 {
		t.Fatalf("effect rows = %d, want 1 (rejected fulfill wrote none)", n)
	}
}

// TestPG_OrderTopup_Regression 充值单 (默认 kind) 零回归: 仍写 payment_credits + billing_events 'payment_credited' + 余额增 + 无订阅授予。
// 判别: 分支误把充值单导向订阅 → 无 credit / Subscription!=nil → 红。
func TestPG_OrderTopup_Regression(t *testing.T) {
	ctx := context.Background()
	f := newPaymentFixture(t, ctx, openPaymentIntegrationPool(t, ctx))
	registerSubscriptionCleanup(f)
	svc := NewService(NewPostgresStore(f.pool))

	created, err := svc.CreateOrder(ctx, CreateOrderInput{
		TenantID: f.tenantA, UserID: f.userA, AmountCents: 500, CurrencyCode: "USD",
		OutTradeNo: "topup-" + f.suffix, ActorAdminID: 1, // OrderKind 缺省 topup
	})
	if err != nil {
		t.Fatalf("create topup order: %v", err)
	}
	res, err := svc.AdminConfirmPaid(ctx, AdminConfirmPaidInput{TenantID: f.tenantA, OrderID: created.Order.ID, ActorAdminID: 1})
	if err != nil {
		t.Fatalf("confirm+fulfill topup: %v", err)
	}
	if res.Subscription != nil {
		t.Fatalf("topup order must not produce subscription grant")
	}
	if res.Credit.ID == 0 {
		t.Fatalf("topup order must produce a credit record")
	}
	if res.BalanceCents != 500 {
		t.Fatalf("balance = %d, want 500", res.BalanceCents)
	}
	if n := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='payment_credited'`, f.tenantA); n != 1 {
		t.Fatalf("payment_credited billing events = %d, want 1 (topup path must still credit)", n)
	}
	if n := f.countInt(`SELECT count(*) FROM subscription_fulfillment_effects WHERE tenant_id=$1`, f.tenantA); n != 0 {
		t.Fatalf("effect rows = %d, want 0 (topup writes no subscription effect)", n)
	}
}
