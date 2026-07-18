// HUAKAI · iKun
//go:build integration_pg

package payment

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 部分冲正保持订单可继续处理，累计达到原入账额时才进入 refunded 终态。
func TestPaymentPostgres_RefundOrderSupportsCumulativeRefunds(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool), WithTestProvider())

	const credit = int64(1500)
	const refund = int64(400)
	created, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: f.tenantA, UserID: f.userA, AmountCents: credit, OutTradeNo: "pay-refund-net-" + f.suffix, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.AdminConfirmPaid(ctx, AdminConfirmPaidInput{TenantID: f.tenantA, OrderID: created.Order.ID, ActorAdminID: 7}); err != nil {
		t.Fatalf("confirm+fulfill: %v", err)
	}
	res, err := svc.RefundOrder(ctx, RefundOrderInput{
		TenantID:       f.tenantA,
		OrderID:        created.Order.ID,
		AmountCents:    refund,
		IdempotencyKey: "refund-net-" + f.suffix,
		Reason:         "operator refund",
		ActorKind:      ActorKindAdmin,
		ActorID:        7,
		RequestID:      "req-refund-net",
	})
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	if res.Order.Status != StatusCompleted || res.BalanceCents != credit-refund ||
		res.CumulativeRefundedCents != refund || res.RemainingRefundableCents != credit-refund {
		t.Fatalf("partial refund result=%+v want completed/balance %d/cumulative %d/remaining %d",
			res, credit-refund, refund, credit-refund)
	}
	bal, err := svc.GetBalance(ctx, f.tenantA, f.userA)
	if err != nil {
		t.Fatalf("get balance: %v", err)
	}
	if bal.AmountCents != credit-refund {
		t.Fatalf("balance=%d want %d (credit-refund)", bal.AmountCents, credit-refund)
	}
	if n := f.countInt(`SELECT count(*) FROM payment_refunds WHERE tenant_id=$1 AND order_id=$2`, f.tenantA, created.Order.ID); n != 1 {
		t.Fatalf("payment_refunds count=%d want 1", n)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO payment_refunds (
    tenant_id, order_id, user_id, amount_cents, requested_amount_cents, require_exact,
    currency, idempotency_key, actor_kind
) VALUES ($1, $2, $3, 1, 1, FALSE, 'EUR', $4, 'admin')`,
		f.tenantA, created.Order.ID, f.userA, "refund-wrong-currency-"+f.suffix); err == nil {
		t.Fatal("direct refund with mismatched currency unexpectedly succeeded")
	}

	var (
		eventType    string
		actualCost   string
		actualSigned string
		refundMatch  bool
	)
	if err := pool.QueryRow(ctx, `
SELECT event_type, actual_cost::text, actual_cost_signed::text, payment_refund_id = $3
FROM billing_events WHERE tenant_id=$1 AND payment_refund_id=$2`,
		f.tenantA, res.Refund.ID, res.Refund.ID).Scan(&eventType, &actualCost, &actualSigned, &refundMatch); err != nil {
		t.Fatalf("read refund billing event: %v", err)
	}
	if eventType != "payment_refunded" || actualCost != "0.00000000" || actualSigned != "-4.00000000" || !refundMatch {
		t.Fatalf("refund billing event type=%q actual=%q signed=%q match=%v, want payment_refunded/0/-4/match",
			eventType, actualCost, actualSigned, refundMatch)
	}

	remaining, err := svc.RefundOrder(ctx, RefundOrderInput{
		TenantID:       f.tenantA,
		OrderID:        created.Order.ID,
		AmountCents:    credit - refund,
		IdempotencyKey: "refund-net-final-" + f.suffix,
		Reason:         "operator refund final",
		ActorKind:      ActorKindAdmin,
		ActorID:        7,
		RequestID:      "req-refund-net-final",
	})
	if err != nil {
		t.Fatalf("final cumulative refund: %v", err)
	}
	if remaining.Order.Status != StatusRefunded || remaining.BalanceCents != 0 ||
		remaining.CumulativeRefundedCents != credit || remaining.RemainingRefundableCents != 0 {
		t.Fatalf("final refund result=%+v want refunded/balance 0/cumulative %d/remaining 0", remaining, credit)
	}
	if n := f.countInt(`SELECT count(*) FROM payment_refunds WHERE tenant_id=$1 AND order_id=$2`, f.tenantA, created.Order.ID); n != 2 {
		t.Fatalf("payment_refunds count=%d want 2", n)
	}
	if n := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='payment_refunded'`, f.tenantA); n != 2 {
		t.Fatalf("payment_refunded event count=%d want 2", n)
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_refunds SET reason=reason WHERE tenant_id=$1 AND id=$2`, f.tenantA, res.Refund.ID); err == nil {
		t.Fatal("append-only payment refund unexpectedly allowed UPDATE")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM payment_refunds WHERE tenant_id=$1 AND id=$2`, f.tenantA, res.Refund.ID); err == nil {
		t.Fatal("append-only payment refund unexpectedly allowed DELETE")
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO payment_refunds (
    tenant_id, order_id, user_id, amount_cents, requested_amount_cents, require_exact,
    currency, idempotency_key, actor_kind
) VALUES ($1, $2, $3, 1, 1, FALSE, $4, $5, 'admin')`,
		f.tenantA, created.Order.ID, f.userA, created.Order.CurrencyCode, "refund-over-cap-direct-"+f.suffix); err == nil {
		t.Fatal("direct refund beyond credited amount unexpectedly succeeded")
	}
	exported, err := svc.ExportRefunds(ctx, RefundExportFilter{TenantID: f.tenantA, Limit: 10})
	if err != nil {
		t.Fatalf("export cumulative refunds: %v", err)
	}
	if len(exported) != 2 || exported[0].BillingEventID <= 0 || exported[1].BillingEventID <= 0 {
		t.Fatalf("exported refunds=%+v want two rows with billing evidence", exported)
	}
	if _, err := svc.RefundOrder(ctx, RefundOrderInput{
		TenantID: f.tenantA, OrderID: created.Order.ID, AmountCents: 1,
		IdempotencyKey: "refund-net-over-" + f.suffix, ActorKind: ActorKindAdmin, ActorID: 7,
	}); !errors.Is(err, ErrRefundExceedsCredit) {
		t.Fatalf("refund beyond cumulative cap err=%v want ErrRefundExceedsCredit", err)
	}
}

func TestPaymentPostgres_RefundExactTargetFillsOnlyRemainingAmount(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool), WithTestProvider())
	order := createCompletedPaymentRefundOrder(t, ctx, svc, f.tenantA, f.userA, 1500, "pay-refund-exact-"+f.suffix)

	if _, err := svc.RefundOrder(ctx, RefundOrderInput{
		TenantID: f.tenantA, OrderID: order.ID, AmountCents: 400,
		IdempotencyKey: "refund-exact-prior-" + f.suffix, ActorKind: ActorKindAdmin, ActorID: 7,
	}); err != nil {
		t.Fatalf("prior refund: %v", err)
	}
	exact := RefundOrderInput{
		TenantID: f.tenantA, OrderID: order.ID, AmountCents: 1500, RequireExact: true,
		IdempotencyKey: "refund-exact-target-" + f.suffix,
		Reason:         "approve remaining", ActorKind: ActorKindAdmin, ActorID: 7, ActorRef: "admin_user:7",
	}
	result, err := svc.RefundOrder(ctx, exact)
	if err != nil {
		t.Fatalf("exact target refund: %v", err)
	}
	if result.Refund.AmountCents != 1100 || result.Refund.RequestedAmountCents != 1500 ||
		!result.Refund.RequireExact || result.Order.Status != StatusRefunded ||
		result.CumulativeRefundedCents != 1500 || result.RemainingRefundableCents != 0 ||
		result.BalanceCents != 0 {
		t.Fatalf("exact target result=%+v", result)
	}
	replay, err := svc.RefundOrder(ctx, exact)
	if err != nil || !replay.Idempotent || replay.Refund.ID != result.Refund.ID || replay.BalanceCents != 0 {
		t.Fatalf("exact replay=%+v err=%v", replay, err)
	}
	if n := f.countInt(`SELECT count(*) FROM payment_refunds WHERE tenant_id=$1 AND order_id=$2`, f.tenantA, order.ID); n != 2 {
		t.Fatalf("refund facts=%d want 2", n)
	}
}

// R2:同 idempotency_key 只接受完整业务请求一致的重放；字段变化必须冲突且零副作用。
func TestPaymentPostgres_RefundOrderIdempotencyRequiresSameBusinessRequest(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool), WithTestProvider())

	created, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: f.tenantA, UserID: f.userA, AmountCents: 1200, OutTradeNo: "pay-refund-dupe-" + f.suffix, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.AdminConfirmPaid(ctx, AdminConfirmPaidInput{TenantID: f.tenantA, OrderID: created.Order.ID, ActorAdminID: 7}); err != nil {
		t.Fatalf("confirm+fulfill: %v", err)
	}
	request := RefundOrderInput{
		TenantID:       f.tenantA,
		OrderID:        created.Order.ID,
		AmountCents:    300,
		IdempotencyKey: "refund-dupe-" + f.suffix,
		Reason:         "operator refund",
		ActorKind:      ActorKindAdmin,
		ActorID:        7,
		ActorRef:       "admin_user:7",
		RequestID:      "refund-request-1",
	}
	first, err := svc.RefundOrder(ctx, request)
	if err != nil {
		t.Fatalf("first refund: %v", err)
	}
	replayRequest := request
	replayRequest.RequestID = "refund-request-retry"
	second, err := svc.RefundOrder(ctx, replayRequest)
	if err != nil {
		t.Fatalf("second refund replay: %v", err)
	}
	if !second.Idempotent || second.Refund.ID != first.Refund.ID || second.Refund.AmountCents != 300 {
		t.Fatalf("second refund should replay first refund, got second=%+v first=%+v", second, first)
	}
	tests := []struct {
		name   string
		mutate func(*RefundOrderInput)
	}{
		{name: "金额变化", mutate: func(in *RefundOrderInput) { in.AmountCents = 999 }},
		{name: "累计目标模式变化", mutate: func(in *RefundOrderInput) { in.RequireExact = true }},
		{name: "原因变化", mutate: func(in *RefundOrderInput) { in.Reason = "different reason" }},
		{name: "执行角色变化", mutate: func(in *RefundOrderInput) { in.ActorKind = ActorKindSystem }},
		{name: "执行人变化", mutate: func(in *RefundOrderInput) { in.ActorID = 8 }},
		{name: "执行身份引用变化", mutate: func(in *RefundOrderInput) { in.ActorRef = "admin_user:8" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conflict := request
			tt.mutate(&conflict)
			result, err := svc.RefundOrder(ctx, conflict)
			if !errors.Is(err, ErrRefundIdempotencyConflict) || result.Refund.ID != 0 {
				t.Fatalf("conflict result=%+v err=%v", result, err)
			}
		})
	}
	if n := f.countInt(`SELECT count(*) FROM payment_refunds WHERE tenant_id=$1 AND order_id=$2`, f.tenantA, created.Order.ID); n != 1 {
		t.Fatalf("payment_refunds count=%d want 1", n)
	}
	if n := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='payment_refunded'`, f.tenantA); n != 1 {
		t.Fatalf("payment_refunded event count=%d want 1", n)
	}
	bal, _ := svc.GetBalance(ctx, f.tenantA, f.userA)
	if bal.AmountCents != 900 {
		t.Fatalf("balance=%d want 900 (refund once)", bal.AmountCents)
	}
}

func TestPaymentPostgres_RefundReplayRejectsMissingBillingEvidence(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool), WithTestProvider())
	order := createCompletedPaymentRefundOrder(t, ctx, svc, f.tenantA, f.userA, 1200, "pay-refund-missing-evidence-"+f.suffix)
	request := RefundOrderInput{
		TenantID: f.tenantA, OrderID: order.ID, AmountCents: 300,
		IdempotencyKey: "refund-missing-evidence-" + f.suffix, Reason: "operator refund",
		ActorKind: ActorKindAdmin, ActorID: 7, ActorRef: "admin_user:7",
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO payment_refunds (
    tenant_id, order_id, user_id, amount_cents, requested_amount_cents, require_exact,
    currency, idempotency_key, reason, actor_kind, actor_id, actor_ref
) VALUES ($1, $2, $3, $4, $4, FALSE, $5, $6, $7, $8, $9, $10)`,
		f.tenantA, order.ID, f.userA, request.AmountCents, order.CurrencyCode,
		request.IdempotencyKey, request.Reason, request.ActorKind, request.ActorID, request.ActorRef); err != nil {
		t.Fatalf("seed refund without billing evidence: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_orders SET status='refunded' WHERE tenant_id=$1 AND id=$2`, f.tenantA, order.ID); err != nil {
		t.Fatalf("mark seeded refund order: %v", err)
	}

	result, err := svc.RefundOrder(ctx, request)
	if !errors.Is(err, ErrRefundFactInvalid) || result.Refund.ID != 0 {
		t.Fatalf("missing evidence replay result=%+v err=%v", result, err)
	}
	if n := f.countInt(`SELECT count(*) FROM payment_refunds WHERE tenant_id=$1 AND order_id=$2`, f.tenantA, order.ID); n != 1 {
		t.Fatalf("payment_refunds count=%d want 1", n)
	}
	if status := paymentOrderStatus(t, ctx, pool, f.tenantA, order.ID); status != StatusRefunded {
		t.Fatalf("order status=%q want refunded", status)
	}
}

func TestPaymentPostgres_RefundReplayRejectsAmbiguousBillingEvidence(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool), WithTestProvider())
	order := createCompletedPaymentRefundOrder(t, ctx, svc, f.tenantA, f.userA, 1200, "pay-refund-duplicate-evidence-"+f.suffix)
	request := RefundOrderInput{
		TenantID: f.tenantA, OrderID: order.ID, AmountCents: 300,
		IdempotencyKey: "refund-duplicate-evidence-" + f.suffix, Reason: "operator refund",
		ActorKind: ActorKindAdmin, ActorID: 7, ActorRef: "admin_user:7",
	}
	first, err := svc.RefundOrder(ctx, request)
	if err != nil {
		t.Fatalf("first refund: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO billing_events (
    tenant_id, event_type, actual_cost, actual_cost_signed,
    stream_state, delivered_token_count, fingerprint, payment_refund_id
) VALUES ($1, 'payment_refunded', 0, $2, 2, 0, $3, $4)`,
		f.tenantA, decimalFromCents(request.AmountCents).Neg(), "duplicate-refund-evidence-"+f.suffix, first.Refund.ID); err != nil {
		t.Fatalf("insert duplicate refund billing evidence: %v", err)
	}

	result, err := svc.RefundOrder(ctx, request)
	if !errors.Is(err, ErrRefundFactInvalid) || result.Refund.ID != 0 {
		t.Fatalf("ambiguous evidence replay result=%+v err=%v", result, err)
	}
	if n := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND payment_refund_id=$2`, f.tenantA, first.Refund.ID); n != 2 {
		t.Fatalf("refund evidence count=%d want 2", n)
	}
}

// 同租户退款幂等键只能绑定一份业务请求，不能跨订单复用后误退第二张订单。
func TestPaymentPostgres_RefundOrderRejectsSameKeyAcrossOrders(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool), WithTestProvider())

	firstOrder := createCompletedPaymentRefundOrder(t, ctx, svc, f.tenantA, f.userA, 1000, "pay-refund-key-a-"+f.suffix)
	secondCreated, err := svc.CreateOrder(ctx, CreateOrderInput{
		TenantID: f.tenantA, UserID: f.userA, AmountCents: 1000,
		OutTradeNo: "pay-refund-key-b-" + f.suffix, ActorAdminID: 7,
	})
	if err != nil {
		t.Fatalf("create second order: %v", err)
	}
	secondFulfilled, err := svc.AdminConfirmPaid(ctx, AdminConfirmPaidInput{TenantID: f.tenantA, OrderID: secondCreated.Order.ID, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("confirm second order: %v", err)
	}
	if secondFulfilled.Order.Status != StatusCompleted || secondFulfilled.BalanceCents != 2000 {
		t.Fatalf("second setup status=%q balance=%d want completed/2000", secondFulfilled.Order.Status, secondFulfilled.BalanceCents)
	}
	secondOrder := secondFulfilled.Order
	key := "refund-shared-order-key-" + f.suffix
	request := RefundOrderInput{
		TenantID: f.tenantA, OrderID: firstOrder.ID, AmountCents: 300, IdempotencyKey: key,
		Reason: "operator refund", ActorKind: ActorKindAdmin, ActorID: 7, ActorRef: "admin_user:7",
	}
	if _, err := svc.RefundOrder(ctx, request); err != nil {
		t.Fatalf("first refund: %v", err)
	}

	conflict := request
	conflict.OrderID = secondOrder.ID
	result, err := svc.RefundOrder(ctx, conflict)
	if !errors.Is(err, ErrRefundIdempotencyConflict) || result.Refund.ID != 0 {
		t.Fatalf("cross-order conflict result=%+v err=%v", result, err)
	}
	missingOrder := request
	missingOrder.OrderID = secondOrder.ID + 1_000_000
	result, err = svc.RefundOrder(ctx, missingOrder)
	if !errors.Is(err, ErrRefundIdempotencyConflict) || result.Refund.ID != 0 {
		t.Fatalf("missing-order conflict result=%+v err=%v", result, err)
	}
	if n := f.countInt(`SELECT count(*) FROM payment_refunds WHERE tenant_id=$1`, f.tenantA); n != 1 {
		t.Fatalf("payment_refunds count=%d want 1", n)
	}
	if n := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='payment_refunded'`, f.tenantA); n != 1 {
		t.Fatalf("payment_refunded event count=%d want 1", n)
	}
	var secondStatus OrderStatus
	if err := pool.QueryRow(ctx, `SELECT status FROM payment_orders WHERE tenant_id=$1 AND id=$2`, f.tenantA, secondOrder.ID).Scan(&secondStatus); err != nil {
		t.Fatalf("read second order: %v", err)
	}
	if secondStatus != StatusCompleted {
		t.Fatalf("second order status=%q want %q", secondStatus, StatusCompleted)
	}
	bal, err := svc.GetBalance(ctx, f.tenantA, f.userA)
	if err != nil {
		t.Fatalf("get balance: %v", err)
	}
	if bal.AmountCents != 1700 {
		t.Fatalf("balance=%d want 1700", bal.AmountCents)
	}
}

func TestPaymentPostgres_ConcurrentSameRefundRequestHasOneEffect(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool), WithTestProvider())
	order := createCompletedPaymentRefundOrder(t, ctx, svc, f.tenantA, f.userA, 1200, "pay-refund-concurrent-"+f.suffix)
	request := RefundOrderInput{
		TenantID: f.tenantA, OrderID: order.ID, AmountCents: 300,
		IdempotencyKey: "refund-concurrent-" + f.suffix, Reason: "operator refund",
		ActorKind: ActorKindAdmin, ActorID: 7, ActorRef: "admin_user:7",
	}

	type outcome struct {
		result RefundResult
		err    error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := svc.RefundOrder(ctx, request)
			outcomes <- outcome{result: result, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(outcomes)

	var applied, replayed int
	var refundID int64
	for got := range outcomes {
		if got.err != nil || got.result.Refund.ID <= 0 {
			t.Fatalf("concurrent refund result=%+v err=%v", got.result, got.err)
		}
		if refundID == 0 {
			refundID = got.result.Refund.ID
		} else if got.result.Refund.ID != refundID {
			t.Fatalf("refund IDs differ first=%d current=%d", refundID, got.result.Refund.ID)
		}
		if got.result.Idempotent {
			replayed++
		} else {
			applied++
		}
	}
	if applied != 1 || replayed != 1 {
		t.Fatalf("applied=%d replayed=%d want 1/1", applied, replayed)
	}
	if n := f.countInt(`SELECT count(*) FROM payment_refunds WHERE tenant_id=$1 AND order_id=$2`, f.tenantA, order.ID); n != 1 {
		t.Fatalf("payment_refunds count=%d want 1", n)
	}
	if n := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='payment_refunded'`, f.tenantA); n != 1 {
		t.Fatalf("payment_refunded event count=%d want 1", n)
	}
	bal, err := svc.GetBalance(ctx, f.tenantA, f.userA)
	if err != nil {
		t.Fatalf("get balance: %v", err)
	}
	if bal.AmountCents != 900 {
		t.Fatalf("balance=%d want 900", bal.AmountCents)
	}
}

func TestPaymentPostgres_ConcurrentDifferentRefundsRespectCumulativeCap(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool), WithTestProvider())
	order := createCompletedPaymentRefundOrder(t, ctx, svc, f.tenantA, f.userA, 1000, "pay-refund-cap-race-"+f.suffix)

	start := make(chan struct{})
	type outcome struct {
		result RefundResult
		err    error
	}
	outcomes := make(chan outcome, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			result, err := svc.RefundOrder(ctx, RefundOrderInput{
				TenantID: f.tenantA, OrderID: order.ID, AmountCents: 700,
				IdempotencyKey: fmt.Sprintf("refund-cap-race-%d-%s", index, f.suffix),
				ActorKind:      ActorKindAdmin,
				ActorID:        7,
			})
			outcomes <- outcome{result: result, err: err}
		}(i)
	}
	close(start)
	wg.Wait()
	close(outcomes)

	var applied, capped int
	for got := range outcomes {
		switch {
		case got.err == nil:
			applied++
			if got.result.CumulativeRefundedCents != 700 || got.result.RemainingRefundableCents != 300 {
				t.Fatalf("applied result=%+v want cumulative 700 and remaining 300", got.result)
			}
		case errors.Is(got.err, ErrRefundExceedsCredit):
			capped++
		default:
			t.Fatalf("unexpected concurrent refund outcome result=%+v err=%v", got.result, got.err)
		}
	}
	if applied != 1 || capped != 1 {
		t.Fatalf("applied=%d capped=%d want 1/1", applied, capped)
	}
	if n := f.countInt(`SELECT count(*) FROM payment_refunds WHERE tenant_id=$1 AND order_id=$2`, f.tenantA, order.ID); n != 1 {
		t.Fatalf("refund facts=%d want 1", n)
	}
	if n := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='payment_refunded'`, f.tenantA); n != 1 {
		t.Fatalf("refund billing events=%d want 1", n)
	}
	if status := paymentOrderStatus(t, ctx, pool, f.tenantA, order.ID); status != StatusCompleted {
		t.Fatalf("order status=%q want completed after partial refund", status)
	}
}

// R3: pending 单不可退款, 且不得写 payment_refunds / payment_refunded / audit refunded。
func TestPaymentPostgres_RefundPendingOrderRejectsWithoutWrites(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool), WithTestProvider())

	created, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: f.tenantA, UserID: f.userA, AmountCents: 700, OutTradeNo: "pay-refund-pending-" + f.suffix, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = svc.RefundOrder(ctx, RefundOrderInput{TenantID: f.tenantA, OrderID: created.Order.ID, AmountCents: 200, IdempotencyKey: "refund-pending-" + f.suffix, ActorKind: ActorKindAdmin, ActorID: 7})
	if !errors.Is(err, ErrOrderNotRefundable) {
		t.Fatalf("refund pending err=%v want ErrOrderNotRefundable", err)
	}
	if n := f.countInt(`SELECT count(*) FROM payment_refunds WHERE tenant_id=$1`, f.tenantA); n != 0 {
		t.Fatalf("payment_refunds count=%d want 0", n)
	}
	if n := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='payment_refunded'`, f.tenantA); n != 0 {
		t.Fatalf("payment_refunded event count=%d want 0", n)
	}
	if n := f.countInt(`SELECT count(*) FROM payment_audit_events WHERE tenant_id=$1 AND event_type='order_refunded'`, f.tenantA); n != 0 {
		t.Fatalf("order_refunded audit count=%d want 0", n)
	}
}

// R4: 退款金额不能超过 legacy 可用余额 (balance-held), 即使未超过原充值额。
// 判别: 若退款仍走无条件负向 delta, $100 充值被消费到 $70 后仍可退 $100,
// user_balances 会被打成 -30, 且 payment_refunds / billing / order status 都会错误写入。
func TestPaymentPostgres_RefundOrderRejectsAboveAvailableWithoutWrites(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool), WithTestProvider())

	created := createCompletedPaymentRefundOrder(t, ctx, svc, f.tenantA, f.userA, 10000, "pay-refund-over-available-"+f.suffix)
	setLegacyPaymentBalance(t, ctx, pool, f.tenantA, f.userA, 7000, 0)

	_, err := svc.RefundOrder(ctx, RefundOrderInput{
		TenantID:       f.tenantA,
		OrderID:        created.ID,
		AmountCents:    10000,
		IdempotencyKey: "refund-over-available-" + f.suffix,
		Reason:         "operator refund",
		ActorKind:      ActorKindAdmin,
		ActorID:        7,
	})
	if !errors.Is(err, ErrRefundExceedsAvailable) {
		t.Fatalf("refund err=%v want ErrRefundExceedsAvailable", err)
	}
	if n := f.countInt(`SELECT count(*) FROM payment_refunds WHERE tenant_id=$1 AND order_id=$2`, f.tenantA, created.ID); n != 0 {
		t.Fatalf("payment_refunds count=%d want 0", n)
	}
	if n := f.countInt(`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND event_type='payment_refunded'`, f.tenantA); n != 0 {
		t.Fatalf("payment_refunded event count=%d want 0", n)
	}
	if n := f.countInt(`SELECT count(*) FROM payment_audit_events WHERE tenant_id=$1 AND event_type='order_refunded'`, f.tenantA); n != 0 {
		t.Fatalf("order_refunded audit count=%d want 0", n)
	}
	if status := paymentOrderStatus(t, ctx, pool, f.tenantA, created.ID); status != StatusCompleted {
		t.Fatalf("order status=%q want completed", status)
	}
	balance, held := legacyPaymentBalanceText(t, ctx, pool, f.tenantA, f.userA)
	if balance != "70.00000000" || held != "0.00000000" {
		t.Fatalf("legacy balance=(%s held %s) want 70/0 unchanged", balance, held)
	}
	derived, err := svc.GetBalance(ctx, f.tenantA, f.userA)
	if err != nil {
		t.Fatalf("get derived balance: %v", err)
	}
	if derived.AmountCents != 10000 {
		t.Fatalf("derived balance=%d want 10000 because no refund fact was written", derived.AmountCents)
	}
}

// R5: 可用余额边界允许退款; exact available 不应被 off-by-one 拒绝。
func TestPaymentPostgres_RefundOrderAllowsAvailableBoundary(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool), WithTestProvider())

	created := createCompletedPaymentRefundOrder(t, ctx, svc, f.tenantA, f.userA, 10000, "pay-refund-boundary-"+f.suffix)
	setLegacyPaymentBalance(t, ctx, pool, f.tenantA, f.userA, 7000, 0)

	res, err := svc.RefundOrder(ctx, RefundOrderInput{
		TenantID:       f.tenantA,
		OrderID:        created.ID,
		AmountCents:    7000,
		IdempotencyKey: "refund-boundary-" + f.suffix,
		Reason:         "operator refund",
		ActorKind:      ActorKindAdmin,
		ActorID:        7,
	})
	if err != nil {
		t.Fatalf("refund at available boundary: %v", err)
	}
	if res.Order.Status != StatusCompleted || res.BalanceCents != 3000 ||
		res.CumulativeRefundedCents != 7000 || res.RemainingRefundableCents != 3000 {
		t.Fatalf("refund result=%+v want completed/balance 3000/cumulative 7000/remaining 3000", res)
	}
	balance, held := legacyPaymentBalanceText(t, ctx, pool, f.tenantA, f.userA)
	if balance != "0.00000000" || held != "0.00000000" {
		t.Fatalf("legacy balance=(%s held %s) want 0/0 after exact available refund", balance, held)
	}
}

// R6: held 必须从可用余额扣除; balance=100, held=30 时最多只能退 70。
func TestPaymentPostgres_RefundOrderUsesBalanceMinusHeldAsAvailable(t *testing.T) {
	ctx := context.Background()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)
	svc := NewService(NewPostgresStore(pool), WithTestProvider())

	blocked := createCompletedPaymentRefundOrder(t, ctx, svc, f.tenantA, f.userA, 10000, "pay-refund-held-blocked-"+f.suffix)
	setLegacyPaymentBalance(t, ctx, pool, f.tenantA, f.userA, 10000, 3000)
	_, err := svc.RefundOrder(ctx, RefundOrderInput{
		TenantID:       f.tenantA,
		OrderID:        blocked.ID,
		AmountCents:    8000,
		IdempotencyKey: "refund-held-blocked-" + f.suffix,
		Reason:         "operator refund",
		ActorKind:      ActorKindAdmin,
		ActorID:        7,
	})
	if !errors.Is(err, ErrRefundExceedsAvailable) {
		t.Fatalf("refund with held err=%v want ErrRefundExceedsAvailable", err)
	}
	balance, held := legacyPaymentBalanceText(t, ctx, pool, f.tenantA, f.userA)
	if balance != "100.00000000" || held != "30.00000000" {
		t.Fatalf("blocked legacy balance=(%s held %s) want 100/30 unchanged", balance, held)
	}

	allowed := createCompletedPaymentRefundOrder(t, ctx, svc, f.tenantB, f.userB, 10000, "pay-refund-held-allowed-"+f.suffix)
	setLegacyPaymentBalance(t, ctx, pool, f.tenantB, f.userB, 10000, 3000)
	if _, err := svc.RefundOrder(ctx, RefundOrderInput{
		TenantID:       f.tenantB,
		OrderID:        allowed.ID,
		AmountCents:    7000,
		IdempotencyKey: "refund-held-allowed-" + f.suffix,
		Reason:         "operator refund",
		ActorKind:      ActorKindAdmin,
		ActorID:        7,
	}); err != nil {
		t.Fatalf("refund exactly balance-held: %v", err)
	}
	balance, held = legacyPaymentBalanceText(t, ctx, pool, f.tenantB, f.userB)
	if balance != "30.00000000" || held != "30.00000000" {
		t.Fatalf("allowed legacy balance=(%s held %s) want 30/30 after refund", balance, held)
	}
}

func createCompletedPaymentRefundOrder(t *testing.T, ctx context.Context, svc *Service, tenantID, userID, amount int64, outTradeNo string) Order {
	t.Helper()
	created, err := svc.CreateOrder(ctx, CreateOrderInput{TenantID: tenantID, UserID: userID, AmountCents: amount, OutTradeNo: outTradeNo, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	fulfilled, err := svc.AdminConfirmPaid(ctx, AdminConfirmPaidInput{TenantID: tenantID, OrderID: created.Order.ID, ActorAdminID: 7})
	if err != nil {
		t.Fatalf("confirm+fulfill: %v", err)
	}
	if fulfilled.Order.Status != StatusCompleted || fulfilled.BalanceCents != amount {
		t.Fatalf("setup status=%q balance=%d want completed/%d", fulfilled.Order.Status, fulfilled.BalanceCents, amount)
	}
	return fulfilled.Order
}

func setLegacyPaymentBalance(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID, balanceCents, heldCents int64) {
	t.Helper()
	tag, err := pool.Exec(ctx, `
UPDATE user_balances
SET balance=$3, held=$4, version=version+1, updated_at=now()
WHERE tenant_id=$1 AND user_id=$2`,
		tenantID, userID, decimalFromCents(balanceCents), decimalFromCents(heldCents))
	if err != nil {
		t.Fatalf("set legacy balance: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("set legacy balance rows=%d want 1", tag.RowsAffected())
	}
}

func legacyPaymentBalanceText(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID int64) (string, string) {
	t.Helper()
	var balance, held string
	if err := pool.QueryRow(ctx, `
SELECT balance::text, held::text
FROM user_balances
WHERE tenant_id=$1 AND user_id=$2`, tenantID, userID).Scan(&balance, &held); err != nil {
		t.Fatalf("read legacy balance: %v", err)
	}
	return balance, held
}

func paymentOrderStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, orderID int64) OrderStatus {
	t.Helper()
	var status OrderStatus
	if err := pool.QueryRow(ctx, `SELECT status FROM payment_orders WHERE tenant_id=$1 AND id=$2`, tenantID, orderID).Scan(&status); err != nil {
		t.Fatalf("read payment order status: %v", err)
	}
	return status
}
