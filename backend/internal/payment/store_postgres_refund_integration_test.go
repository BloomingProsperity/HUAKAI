// HUAKAI · iKun
//go:build integration_pg

package payment

import (
	"context"
	"errors"
	"testing"
)

// R1: 已入账充值单退款一次 → payment_refunds + payment_refunded billing event + 派生余额 credit-refund。
func TestPaymentPostgres_RefundOrderWritesEventAndNetBalance(t *testing.T) {
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
	if res.Order.Status != StatusRefunded || res.BalanceCents != credit-refund {
		t.Fatalf("refund result status=%q balance=%d want refunded/%d", res.Order.Status, res.BalanceCents, credit-refund)
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
}

// R2: 同 idempotency_key 重放必须返回既有 refund, 不重复插 refund / billing_event, 余额只扣一次。
func TestPaymentPostgres_RefundOrderIdempotencyDoesNotDoubleDeduct(t *testing.T) {
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
	first, err := svc.RefundOrder(ctx, RefundOrderInput{TenantID: f.tenantA, OrderID: created.Order.ID, AmountCents: 300, IdempotencyKey: "refund-dupe-" + f.suffix, ActorKind: ActorKindAdmin, ActorID: 7})
	if err != nil {
		t.Fatalf("first refund: %v", err)
	}
	second, err := svc.RefundOrder(ctx, RefundOrderInput{TenantID: f.tenantA, OrderID: created.Order.ID, AmountCents: 999, IdempotencyKey: "refund-dupe-" + f.suffix, ActorKind: ActorKindAdmin, ActorID: 7})
	if err != nil {
		t.Fatalf("second refund replay: %v", err)
	}
	if !second.Idempotent || second.Refund.ID != first.Refund.ID || second.Refund.AmountCents != 300 {
		t.Fatalf("second refund should replay first refund, got second=%+v first=%+v", second, first)
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
