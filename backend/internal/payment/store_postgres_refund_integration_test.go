// HUAKAI · iKun
//go:build integration_pg

package payment

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
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
	if res.Order.Status != StatusRefunded || res.BalanceCents != 3000 {
		t.Fatalf("refund result status=%q balance=%d want refunded/3000", res.Order.Status, res.BalanceCents)
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
