// HUAKAI · iKun
//go:build integration_pg

// 支付 P1 真 PG 判别测试。每个测试守一个具体钱路径缺陷, fixture 设计成 mutation 即变红:
//   T1 重复 out_trade_no / 重复确认 不双账
//   T2 并发履约只有一个 CAS 胜出 (32 goroutine barrier)
//   T3 入账 billing_event 字段 + 互斥列 + 派生余额精确
//   T4 跨租户隔离 (同 out_trade_no 不串租户/不错账)
//   T5 recharging 断点续跑只入账一次
//   T6 履约要求 paid/recharging 状态 (pending 越权履约被拒)
//   T7 操作审计轨迹落库
//   T8 同用户待支付订单上限在并发事务下不可绕过

package payment

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// T1: 重复建单 + 重复确认 → 仅一张订单 / 一条 credit / 一条 payment_credited / 余额只增一次。
func TestPaymentPostgres_DuplicateOutTradeNoDoesNotDoubleCredit(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool), WithTestProvider())

	outNo := "pay-p1-dupe-" + f.suffix
	const amount = int64(1234)
	in := CreateOrderInput{TenantID: f.tenantA, UserID: f.userA, AmountCents: amount, OutTradeNo: outNo, ActorAdminID: 7}

	r1, err := svc.CreateOrder(ctx, in)
	if err != nil || r1.Idempotent {
		t.Fatalf("first create: err=%v idempotent=%v", err, r1.Idempotent)
	}
	r2, err := svc.CreateOrder(ctx, in)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if !r2.Idempotent || r2.Order.ID != r1.Order.ID {
		t.Fatalf("duplicate create should replay same order: idempotent=%v id1=%d id2=%d", r2.Idempotent, r1.Order.ID, r2.Order.ID)
	}

	res1, err := svc.AdminConfirmPaid(ctx, AdminConfirmPaidInput{TenantID: f.tenantA, OrderID: r1.Order.ID, ActorAdminID: 7})
	if err != nil || res1.Idempotent {
		t.Fatalf("first confirm: err=%v idempotent=%v", err, res1.Idempotent)
	}
	if res1.BalanceCents != amount {
		t.Fatalf("balance after first fulfill = %d, want %d", res1.BalanceCents, amount)
	}
	res2, err := svc.AdminConfirmPaid(ctx, AdminConfirmPaidInput{TenantID: f.tenantA, OrderID: r1.Order.ID, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("second confirm: %v", err)
	}
	// 自证: 重复确认后的余额必须与单次确认相同 (而非翻倍)。
	if !res2.Idempotent || res2.BalanceCents != amount {
		t.Fatalf("duplicate confirm doubled credit: idempotent=%v balance=%d want %d", res2.Idempotent, res2.BalanceCents, amount)
	}

	if n := f.countInt(`SELECT count(*) FROM payment_orders WHERE tenant_id=$1 AND out_trade_no=$2`, f.tenantA, outNo); n != 1 {
		t.Fatalf("order count = %d, want 1", n)
	}
	if n := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1 AND payment_order_id=$2`, f.tenantA, r1.Order.ID); n != 1 {
		t.Fatalf("credit count = %d, want 1", n)
	}
	if n := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='payment_credited'`, f.tenantA); n != 1 {
		t.Fatalf("payment_credited event count = %d, want 1", n)
	}
}

// T2: 同一 paid 订单 32 goroutine 并发履约 → 恰一次入账, 余额增一次。
func TestPaymentPostgres_ConcurrentFulfillOnlyOneCASSucceeds(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	svc := NewService(store, WithTestProvider())

	const amount = int64(789)
	r, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: f.tenantA, UserID: f.userA, AmountCents: amount, OutTradeNo: "pay-p1-cas-" + f.suffix, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 推进到 paid (不履约), 制造并发履约竞态。
	if _, err := store.ConfirmPaid(ctx, confirmRecord{TenantID: f.tenantA, OrderID: r.Order.ID, AdminID: 7, Now: time.Now().UTC()}); err != nil {
		t.Fatalf("confirm paid: %v", err)
	}

	const goroutines = 32 // 注释与实际 N 必须一致
	var wg sync.WaitGroup
	barrier := make(chan struct{})
	var mu sync.Mutex
	creditedCount := 0
	var callerErrs []error
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			res, err := svc.Fulfill(ctx, FulfillInput{TenantID: f.tenantA, OrderID: r.Order.ID, ActorKind: ActorKindAdmin, ActorID: 7})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				callerErrs = append(callerErrs, err)
				return
			}
			if !res.Idempotent {
				creditedCount++
			}
		}()
	}
	close(barrier)
	wg.Wait()

	// 幂等并发契约: 每一路都必须成功 (要么入账, 要么幂等返回), 没有一路该报错。
	// 守住"loser 优雅幂等"而非"loser 报错"的回归 (mutation: loser 返回 error → callerErrs 非空 → 红)。
	if len(callerErrs) != 0 {
		t.Fatalf("concurrent fulfill returned %d unexpected caller errors (all should credit or idempotent-succeed): %v", len(callerErrs), callerErrs[0])
	}
	// 自证: 32 路并发只能有一路真入账。
	if creditedCount != 1 {
		t.Fatalf("non-idempotent fulfill count = %d, want exactly 1", creditedCount)
	}
	if n := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1 AND payment_order_id=$2`, f.tenantA, r.Order.ID); n != 1 {
		t.Fatalf("credit count = %d, want 1", n)
	}
	if n := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='payment_credited'`, f.tenantA); n != 1 {
		t.Fatalf("payment_credited event count = %d, want 1", n)
	}
	bal, err := svc.GetBalance(ctx, f.tenantA, f.userA)
	if err != nil || bal.AmountCents != amount {
		t.Fatalf("balance = %d (err=%v), want %d", bal.AmountCents, err, amount)
	}
}

// T8: 同一用户并发创建六张、上限为一张的待支付订单，数据库只能提交一张。
// 这里直接并发调用建单底座，绕过 HTTP 层的提前统计，专门证明事务锁与事务内复核
// 能挡住两个请求同时读到旧统计值的竞态。
func TestPaymentPostgres_ConcurrentPendingLimitCannotBeBypassed(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool), WithTestProvider())

	const workers = 6
	barrier := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	rejected := 0
	var unexpected []error
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-barrier
			_, err := svc.CreateOrder(ctx, CreateOrderInput{
				TenantID: f.tenantA, UserID: f.userA, AmountCents: 100,
				OutTradeNo:   "pay-pending-cap-" + f.suffix + "-" + stringID(i),
				ProviderKind: ProviderTest, RechargeMaxPending: 1,
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				success++
			case errors.Is(err, ErrPendingLimit):
				rejected++
			default:
				unexpected = append(unexpected, err)
			}
		}(i)
	}
	close(barrier)
	wg.Wait()

	if len(unexpected) != 0 {
		t.Fatalf("出现非预期建单错误: %v", unexpected[0])
	}
	if success != 1 || rejected != workers-1 {
		t.Fatalf("success/rejected=%d/%d，期望 1/%d", success, rejected, workers-1)
	}
	if count := f.countInt(`SELECT count(*) FROM payment_orders WHERE tenant_id=$1 AND user_id=$2 AND status='pending'`, f.tenantA, f.userA); count != 1 {
		t.Fatalf("数据库待支付订单=%d，期望 1", count)
	}
}

// T3: 入账 billing_event 字段精确 (金额方向/互斥列) + 派生余额精确增。
func TestPaymentPostgres_CreditBillingEventAndDerivedBalanceMatch(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool), WithTestProvider())

	const amount = int64(2550) // 25.50
	before, err := svc.GetBalance(ctx, f.tenantA, f.userA)
	if err != nil {
		t.Fatalf("balance before: %v", err)
	}
	r, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: f.tenantA, UserID: f.userA, AmountCents: amount, OutTradeNo: "pay-p1-credit-" + f.suffix, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	res, err := svc.AdminConfirmPaid(ctx, AdminConfirmPaidInput{TenantID: f.tenantA, OrderID: r.Order.ID, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("confirm+fulfill: %v", err)
	}

	var (
		eventType    string
		actualCost   string
		actualSigned string
		claimNull    bool
		voucherNull  bool
		creditMatch  bool
	)
	if err := pool.QueryRow(ctx, `
SELECT event_type, actual_cost::text, actual_cost_signed::text,
	claim_id IS NULL, voucher_redemption_id IS NULL, payment_credit_id = $3
FROM billing_events WHERE tenant_id=$1 AND payment_credit_id=$2`,
		f.tenantA, res.Credit.ID, res.Credit.ID).Scan(&eventType, &actualCost, &actualSigned, &claimNull, &voucherNull, &creditMatch); err != nil {
		t.Fatalf("read billing event: %v", err)
	}
	if eventType != "payment_credited" {
		t.Fatalf("event_type = %q, want payment_credited", eventType)
	}
	// 金额方向: 入账是正向 signed (mutation 写负/零 → 余额错 → 红)。
	if actualCost != "25.50000000" || actualSigned != "25.50000000" {
		t.Fatalf("actual_cost=%q actual_cost_signed=%q, want 25.50000000", actualCost, actualSigned)
	}
	if !claimNull || !voucherNull || !creditMatch {
		t.Fatalf("mutual-exclusion columns wrong: claimNull=%v voucherNull=%v creditMatch=%v", claimNull, voucherNull, creditMatch)
	}
	// 自证: 余额增量必须恰等于入账金额 (mutation 余额漏算 payment → 增量 != amount → 红)。
	after, err := svc.GetBalance(ctx, f.tenantA, f.userA)
	if err != nil {
		t.Fatalf("balance after: %v", err)
	}
	if got := after.AmountCents - before.AmountCents; got != amount {
		t.Fatalf("balance delta = %d, want %d", got, amount)
	}
}

// T4: 跨租户隔离 — 同 out_trade_no, 只履约 A; B 余额=0, B 查 A 单 not found, B 无 payment_credited。
func TestPaymentPostgres_TenantIsolationForSameOutTradeNo(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	svc := NewService(store, WithTestProvider())

	shared := "pay-p1-shared-" + f.suffix
	const amountA = int64(321)
	const amountB = int64(456)
	rA, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: f.tenantA, UserID: f.userA, AmountCents: amountA, OutTradeNo: shared, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	if _, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: f.tenantB, UserID: f.userB, AmountCents: amountB, OutTradeNo: shared, ActorAdminID: 9}); err != nil {
		t.Fatalf("create B (same out_trade_no, different tenant must be allowed): %v", err)
	}
	if _, err := svc.AdminConfirmPaid(ctx, AdminConfirmPaidInput{TenantID: f.tenantA, OrderID: rA.Order.ID, ActorAdminID: 7}); err != nil {
		t.Fatalf("confirm A: %v", err)
	}

	balA, _ := svc.GetBalance(ctx, f.tenantA, f.userA)
	balB, _ := svc.GetBalance(ctx, f.tenantB, f.userB)
	if balA.AmountCents != amountA {
		t.Fatalf("tenant A balance = %d, want %d", balA.AmountCents, amountA)
	}
	if balB.AmountCents != 0 {
		t.Fatalf("tenant B balance = %d, want 0 (cross-tenant leak)", balB.AmountCents)
	}
	// 判别性核心: A 的 user_id 落在 B 租户下查余额必须为 0。
	// 若 balance query 漏 tenant 谓词, 会把 A 的入账按 user_id 算进来 → != 0 → 红。
	// (A/B 的 user_id 本不同, 仅查 balB 不足以判别 tenant 谓词缺失。)
	leak, err := svc.GetBalance(ctx, f.tenantB, f.userA)
	if err != nil {
		t.Fatalf("cross-tenant balance probe: %v", err)
	}
	if leak.AmountCents != 0 {
		t.Fatalf("GetBalance(tenantB, userA) = %d, want 0 (missing tenant predicate leaks A's credit)", leak.AmountCents)
	}
	// B 拿 A 的订单 id 查询必须 not found (tenant-scoped)。
	if _, err := store.GetOrder(ctx, f.tenantB, rA.Order.ID); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("cross-tenant GetOrder err = %v, want ErrOrderNotFound", err)
	}
	if n := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='payment_credited'`, f.tenantB); n != 0 {
		t.Fatalf("tenant B payment_credited count = %d, want 0", n)
	}
}

// T5: recharging 断点续跑 — 只跑 phase1 推进 recharging (无 credit), 再 Fulfill 续跑只入账一次。
func TestPaymentPostgres_RechargingRetryCompletesOnce(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	svc := NewService(store, WithTestProvider())

	const amount = int64(999)
	r, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: f.tenantA, UserID: f.userA, AmountCents: amount, OutTradeNo: "pay-p1-retry-" + f.suffix, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.ConfirmPaid(ctx, confirmRecord{TenantID: f.tenantA, OrderID: r.Order.ID, AdminID: 7, Now: time.Now().UTC()}); err != nil {
		t.Fatalf("confirm paid: %v", err)
	}
	// 模拟崩溃点: 只跑 phase1 (推进 recharging, 不写 credit)。
	if _, _, err := store.BeginFulfill(ctx, fulfillRecord{TenantID: f.tenantA, OrderID: r.Order.ID, ActorKind: ActorKindAdmin, ActorID: 7, Now: time.Now().UTC()}); err != nil {
		t.Fatalf("begin fulfill phase1: %v", err)
	}
	got, err := store.GetOrder(ctx, f.tenantA, r.Order.ID)
	if err != nil || got.Status != StatusRecharging {
		t.Fatalf("after phase1 status = %q (err=%v), want recharging", got.Status, err)
	}
	if n := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1 AND payment_order_id=$2`, f.tenantA, r.Order.ID); n != 0 {
		t.Fatalf("credit exists before phase2: count=%d, want 0", n)
	}
	// 续跑: Fulfill 必须从 recharging 续完。
	res, err := svc.Fulfill(ctx, FulfillInput{TenantID: f.tenantA, OrderID: r.Order.ID, ActorKind: ActorKindAdmin, ActorID: 7})
	if err != nil {
		t.Fatalf("resume fulfill: %v", err)
	}
	if res.Order.Status != StatusCompleted || res.BalanceCents != amount {
		t.Fatalf("resume result status=%q balance=%d, want completed/%d", res.Order.Status, res.BalanceCents, amount)
	}
	if n := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1 AND payment_order_id=$2`, f.tenantA, r.Order.ID); n != 1 {
		t.Fatalf("credit count after resume = %d, want 1", n)
	}
}

// T6: 状态机越权 — pending 订单直接 Fulfill 必须被拒, 无入账。
func TestPaymentPostgres_FulfillRequiresPaidOrRecharging(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool), WithTestProvider())

	const amount = int64(500)
	r, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: f.tenantA, UserID: f.userA, AmountCents: amount, OutTradeNo: "pay-p1-state-" + f.suffix, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 未确认 (pending) 直接履约 → 拒。
	if _, err := svc.Fulfill(ctx, FulfillInput{TenantID: f.tenantA, OrderID: r.Order.ID, ActorKind: ActorKindAdmin, ActorID: 7}); !errors.Is(err, ErrOrderNotFulfillable) {
		t.Fatalf("fulfill pending err = %v, want ErrOrderNotFulfillable", err)
	}
	// 自证: 越权履约不得产生任何入账。
	if n := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1 AND payment_order_id=$2`, f.tenantA, r.Order.ID); n != 0 {
		t.Fatalf("credit count = %d, want 0 (illegal fulfill credited)", n)
	}
	if n := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='payment_credited'`, f.tenantA); n != 0 {
		t.Fatalf("payment_credited count = %d, want 0", n)
	}
	bal, _ := svc.GetBalance(ctx, f.tenantA, f.userA)
	if bal.AmountCents != 0 {
		t.Fatalf("balance = %d, want 0", bal.AmountCents)
	}
}

// T7: 操作审计轨迹 — 建单/确认/履约后审计表含全链路事件 + actor 归属。
func TestPaymentPostgres_PaymentAuditTrailRecorded(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool), WithTestProvider())

	const adminID = int64(42)
	r, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: f.tenantA, UserID: f.userA, AmountCents: 1000, OutTradeNo: "pay-p1-audit-" + f.suffix, ActorAdminID: adminID})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.AdminConfirmPaid(ctx, AdminConfirmPaidInput{TenantID: f.tenantA, OrderID: r.Order.ID, ActorAdminID: adminID, ConfirmReason: "manual ok"}); err != nil {
		t.Fatalf("confirm+fulfill: %v", err)
	}
	events, err := svc.ListAuditEvents(ctx, f.tenantA, r.Order.ID)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	seen := map[string]AuditEvent{}
	for _, ev := range events {
		seen[ev.EventType] = ev
	}
	for _, want := range []string{AuditOrderCreated, AuditPaidConfirmed, AuditFulfillmentStarted, AuditCredited} {
		if _, ok := seen[want]; !ok {
			t.Fatalf("audit trail missing %q; got %v", want, keysOf(seen))
		}
	}
	// actor 归属: credited / paid_confirmed 必须记到操作管理员。
	if seen[AuditCredited].ActorID != adminID {
		t.Fatalf("credited audit actor = %d, want %d", seen[AuditCredited].ActorID, adminID)
	}
	if seen[AuditPaidConfirmed].ActorID != adminID {
		t.Fatalf("paid_confirmed audit actor = %d, want %d", seen[AuditPaidConfirmed].ActorID, adminID)
	}
}

// T8: 过期 pending 订单确认被拒 — 标记 expired, 不入账 (防 stale 单无限期履约)。
func TestPaymentPostgres_ExpiredPendingOrderRejected(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	store := NewPostgresStore(pool)

	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	o, _, err := store.CreateOrder(ctx, createOrderRecord{
		TenantID: f.tenantA, UserID: f.userA, OutTradeNo: "pay-p1-exp-" + f.suffix,
		AmountCents: 600, CurrencyCode: "USD", ProviderKind: ProviderManual, ExpiresAt: &past, Now: now,
	})
	if err != nil {
		t.Fatalf("create expired order: %v", err)
	}
	// 已过期的 pending 订单确认必须被拒。
	if _, err := store.ConfirmPaid(ctx, confirmRecord{TenantID: f.tenantA, OrderID: o.ID, AdminID: 7, Now: now}); !errors.Is(err, ErrOrderNotConfirmable) {
		t.Fatalf("confirm expired order err = %v, want ErrOrderNotConfirmable", err)
	}
	// 自证: 过期单不得入账, 且应被标记 expired (mutation 去掉过期检查 → 入账 → 红)。
	got, err := store.GetOrder(ctx, f.tenantA, o.ID)
	if err != nil || got.Status != StatusExpired {
		t.Fatalf("expired order status = %q (err=%v), want expired", got.Status, err)
	}
	if n := f.countInt(`SELECT count(*) FROM payment_credits WHERE tenant_id=$1 AND payment_order_id=$2`, f.tenantA, o.ID); n != 0 {
		t.Fatalf("credit count = %d, want 0 (expired order credited)", n)
	}
	bal, _ := store.UserBalanceCents(ctx, f.tenantA, f.userA)
	if bal != 0 {
		t.Fatalf("balance = %d, want 0", bal)
	}
}

// PAY-075: 合规确认字段是订单纯记录元数据。带 terms_version / accepted_at / by / ip 建单后,
// GetOrder 必须原样读回。mutation: insert 列表漏掉 compliance 字段或恒写 NULL → terms_version 空 → 红。
func TestOrderComplianceRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	acceptedAt := time.Date(2026, 6, 8, 12, 30, 45, 123456000, time.UTC)
	svc := NewService(store, WithTestProvider(), WithClock(func() time.Time { return acceptedAt }))

	created, err := svc.CreateOrder(ctx, CreateOrderInput{
		TenantID:               f.tenantA,
		UserID:                 f.userA,
		AmountCents:            1200,
		OutTradeNo:             "pay-075-compliance-" + f.suffix,
		ProviderKind:           ProviderTest,
		ComplianceTermsVersion: "v1",
		ComplianceAcceptedAt:   &acceptedAt,
		ComplianceAcceptedBy:   f.userA,
		ComplianceAcceptedIP:   "198.51.100.23",
	})
	if err != nil {
		t.Fatalf("create order with compliance: %v", err)
	}
	got, err := store.GetOrder(ctx, f.tenantA, created.Order.ID)
	if err != nil {
		t.Fatalf("get order: %v", err)
	}
	if got.ComplianceTermsVersion != "v1" {
		t.Fatalf("terms_version=%q want v1", got.ComplianceTermsVersion)
	}
	if got.ComplianceAcceptedAt == nil || !got.ComplianceAcceptedAt.Equal(acceptedAt) {
		t.Fatalf("accepted_at=%v want %s", got.ComplianceAcceptedAt, acceptedAt.Format(time.RFC3339Nano))
	}
	if got.ComplianceAcceptedBy != f.userA {
		t.Fatalf("accepted_by=%d want %d", got.ComplianceAcceptedBy, f.userA)
	}
	if got.ComplianceAcceptedIP != "198.51.100.23" {
		t.Fatalf("accepted_ip=%q want 198.51.100.23", got.ComplianceAcceptedIP)
	}
}

// PAY-075: 未带合规确认的订单必须保持默认行为: 四字段为空/零, 且确认履约的 credit 与余额增量
// 完全等于订单金额。mutation: fulfillment 金额依赖 compliance → credit 或 balance delta 变红。
func TestOrderWithoutComplianceUnchanged(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	svc := NewService(store, WithTestProvider())

	const amount = int64(4321)
	before, err := svc.GetBalance(ctx, f.tenantA, f.userA)
	if err != nil {
		t.Fatalf("balance before: %v", err)
	}
	created, err := svc.CreateOrder(ctx, CreateOrderInput{
		TenantID:     f.tenantA,
		UserID:       f.userA,
		AmountCents:  amount,
		OutTradeNo:   "pay-075-no-compliance-" + f.suffix,
		ProviderKind: ProviderTest,
	})
	if err != nil {
		t.Fatalf("create order without compliance: %v", err)
	}
	if created.Order.ComplianceTermsVersion != "" || created.Order.ComplianceAcceptedAt != nil ||
		created.Order.ComplianceAcceptedBy != 0 || created.Order.ComplianceAcceptedIP != "" {
		t.Fatalf("new order compliance fields = version=%q at=%v by=%d ip=%q, want all empty/zero",
			created.Order.ComplianceTermsVersion, created.Order.ComplianceAcceptedAt,
			created.Order.ComplianceAcceptedBy, created.Order.ComplianceAcceptedIP)
	}
	stored, err := store.GetOrder(ctx, f.tenantA, created.Order.ID)
	if err != nil {
		t.Fatalf("get order: %v", err)
	}
	if stored.ComplianceTermsVersion != "" || stored.ComplianceAcceptedAt != nil ||
		stored.ComplianceAcceptedBy != 0 || stored.ComplianceAcceptedIP != "" {
		t.Fatalf("stored compliance fields = version=%q at=%v by=%d ip=%q, want all empty/zero",
			stored.ComplianceTermsVersion, stored.ComplianceAcceptedAt,
			stored.ComplianceAcceptedBy, stored.ComplianceAcceptedIP)
	}
	fulfilled, err := svc.AdminConfirmPaid(ctx, AdminConfirmPaidInput{
		TenantID:     f.tenantA,
		OrderID:      created.Order.ID,
		ActorAdminID: 7,
	})
	if err != nil {
		t.Fatalf("confirm+fulfill without compliance: %v", err)
	}
	if fulfilled.Credit.AmountCents != amount {
		t.Fatalf("credit amount=%d want %d", fulfilled.Credit.AmountCents, amount)
	}
	if got := fulfilled.BalanceCents - before.AmountCents; got != amount {
		t.Fatalf("balance delta=%d want %d", got, amount)
	}
	var creditAmount int64
	if err := pool.QueryRow(ctx, `
SELECT amount_cents FROM payment_credits WHERE tenant_id=$1 AND payment_order_id=$2`,
		f.tenantA, created.Order.ID).Scan(&creditAmount); err != nil {
		t.Fatalf("read credit amount: %v", err)
	}
	if creditAmount != amount {
		t.Fatalf("stored credit amount=%d want %d", creditAmount, amount)
	}
}

func keysOf(m map[string]AuditEvent) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
