//go:build integration_pg

package payment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

func TestPostgresStoreHandleCallbackCompletesAndCreditsOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)

	svc := NewService(NewPostgresStore(pool))
	order := openCallbackOrder(t, ctx, svc, f.tenantA, f.userA, "trade-callback-valid")
	input := signedCallback(f.tenantA, order.OutTradeNo, "evt_callback_valid", "50.00000000")

	result, err := svc.HandleCallback(ctx, input)
	if err != nil {
		t.Fatalf("HandleCallback valid: %v", err)
	}
	if result.Idempotent {
		t.Fatal("first callback returned Idempotent=true, want false")
	}
	if result.HTTPStatus != 200 {
		t.Fatalf("HTTPStatus=%d want 200", result.HTTPStatus)
	}
	assertPaymentOrderStatus(t, ctx, pool, f.tenantA, order.ID, StatusCompleted)
	assertUserBalanceText(t, ctx, pool, f.tenantA, f.userA, "50.00000000")
	assertPaymentCreditedEventCount(t, ctx, pool, f.tenantA, order.ID, 1)
	assertPaymentAuditEventCount(t, ctx, pool, f.tenantA, order.ID, AuditCredited, 1)

	replay, err := svc.HandleCallback(ctx, input)
	if err != nil {
		t.Fatalf("HandleCallback replay: %v", err)
	}
	if !replay.Idempotent {
		t.Fatal("replay Idempotent=false, want true")
	}
	assertPaymentOrderStatus(t, ctx, pool, f.tenantA, order.ID, StatusCompleted)
	assertUserBalanceText(t, ctx, pool, f.tenantA, f.userA, "50.00000000")
	assertPaymentCreditedEventCount(t, ctx, pool, f.tenantA, order.ID, 1)
}

func TestPostgresStoreHandleCallbackRejectsUnderpaymentAndAuditsMismatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentIntegrationPool(t, ctx)
	f := newPaymentFixture(t, ctx, pool)

	svc := NewService(NewPostgresStore(pool))
	order := openCallbackOrder(t, ctx, svc, f.tenantA, f.userA, "trade-callback-underpay")
	input := signedCallback(f.tenantA, order.OutTradeNo, "evt_callback_underpay", "5.00000000")

	result, err := svc.HandleCallback(ctx, input)
	if !errors.Is(err, ErrPaymentAmountMismatch) {
		t.Fatalf("HandleCallback underpay err=%v want ErrPaymentAmountMismatch", err)
	}
	if result.HTTPStatus != 200 {
		t.Fatalf("HTTPStatus=%d want 200 for verified anti-tamper rejection", result.HTTPStatus)
	}
	assertPaymentOrderStatus(t, ctx, pool, f.tenantA, order.ID, StatusPending)
	assertNoUserBalance(t, ctx, pool, f.tenantA, f.userA)
	assertPaymentCreditedEventCount(t, ctx, pool, f.tenantA, order.ID, 0)
	assertPaymentAuditReasonCount(t, ctx, pool, f.tenantA, order.ID, AuditReasonAmountMismatch, 1)
}

func openCallbackOrder(t *testing.T, ctx context.Context, svc *Service, tenantID, userID int64, tradeNo string) Order {
	t.Helper()
	res, err := svc.OpenRecharge(ctx, OpenInput{
		TenantID:          tenantID,
		UserID:            userID,
		ExternalTradeNo:   tradeNo,
		Provider:          "hmacpay",
		Amount:            decimal.RequireFromString("50.00000000"),
		CurrencyCode:      "USD",
		MaxPendingPerUser: 3,
		DailyAmountLimit:  decimal.RequireFromString("500.00000000"),
		Now:               time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("OpenRecharge callback order: %v", err)
	}
	return res.Order
}

func signedCallback(tenantID int64, tradeNo, eventID, amount string) CallbackInput {
	input := CallbackInput{
		TenantID:        tenantID,
		Provider:        "hmacpay",
		ExternalTradeNo: tradeNo,
		ProviderEventID: eventID,
		PaidAmount:      decimal.RequireFromString(amount),
		CurrencyCode:    "USD",
		Timestamp:       time.Unix(1_800_001_000, 0).UTC(),
		Secret:          "mock-secret",
	}
	input.Signature = mockCallbackSignature(input, input.Secret)
	return input
}

func assertPaymentOrderStatus(t *testing.T, ctx context.Context, pool queryPool, tenantID, orderID int64, want OrderStatus) {
	t.Helper()
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM payment_orders WHERE tenant_id=$1 AND id=$2`,
		tenantID, orderID,
	).Scan(&status); err != nil {
		t.Fatalf("read payment order status: %v", err)
	}
	if OrderStatus(status) != want {
		t.Fatalf("order status=%q want %q", status, want)
	}
}

func assertUserBalanceText(t *testing.T, ctx context.Context, pool queryPool, tenantID, userID int64, want string) {
	t.Helper()
	var balance string
	if err := pool.QueryRow(ctx,
		`SELECT balance::text FROM user_balances WHERE tenant_id=$1 AND user_id=$2`,
		tenantID, userID,
	).Scan(&balance); err != nil {
		t.Fatalf("read user balance: %v", err)
	}
	if balance != want {
		t.Fatalf("user balance=%q want %q", balance, want)
	}
}

func assertNoUserBalance(t *testing.T, ctx context.Context, pool queryPool, tenantID, userID int64) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM user_balances WHERE tenant_id=$1 AND user_id=$2)`,
		tenantID, userID,
	).Scan(&exists); err != nil {
		t.Fatalf("read user balance existence: %v", err)
	}
	if exists {
		t.Fatal("user balance exists after rejected callback; want no credit row")
	}
}

func assertPaymentCreditedEventCount(t *testing.T, ctx context.Context, pool queryPool, tenantID, orderID int64, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*)
FROM billing_events be
JOIN payment_credits pc
  ON pc.tenant_id = be.tenant_id
 AND pc.id = be.payment_credit_id
WHERE be.tenant_id=$1
  AND pc.payment_order_id=$2
  AND be.event_type='payment_credited'`,
		tenantID, orderID,
	).Scan(&count); err != nil {
		t.Fatalf("count payment_credited events: %v", err)
	}
	if count != want {
		t.Fatalf("payment_credited events=%d want %d", count, want)
	}
}

func assertPaymentAuditEventCount(t *testing.T, ctx context.Context, pool queryPool, tenantID, orderID int64, eventType string, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM payment_audit_events WHERE tenant_id=$1 AND payment_order_id=$2 AND event_type=$3`,
		tenantID, orderID, eventType,
	).Scan(&count); err != nil {
		t.Fatalf("count payment audit event %s: %v", eventType, err)
	}
	if count != want {
		t.Fatalf("payment audit event %s count=%d want %d", eventType, count, want)
	}
}

func assertPaymentAuditReasonCount(t *testing.T, ctx context.Context, pool queryPool, tenantID, orderID int64, reason string, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*)
FROM payment_audit_events
WHERE tenant_id=$1
  AND payment_order_id=$2
  AND event_type='fulfillment_failed'
  AND reason_class=$3`,
		tenantID, orderID, reason,
	).Scan(&count); err != nil {
		t.Fatalf("count payment audit reason %s: %v", reason, err)
	}
	if count != want {
		t.Fatalf("payment audit reason %s count=%d want %d", reason, count, want)
	}
}

type queryPool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}
