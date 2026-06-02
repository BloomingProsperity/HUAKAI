//go:build integration_pg

package subscription

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

func TestPaymentBridgeActivatesSubscriptionAndKeepsRechargeLink(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openSubscriptionPool(t, ctx)
	tenantID, userID := seedSubscriptionUser(t, ctx, pool, "callback-link")

	svc := newSubscriptionIntegrationService(pool, "callback-link", time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC))
	plan := createIntegrationPlan(t, ctx, svc, tenantID, "link-plan", decimal.RequireFromString("50.00000000"), DurationDay, 30)
	order := createIntegrationOrder(t, ctx, svc, tenantID, userID, plan.ID, "mock")

	bridge := NewPaymentBridge(payment.NewService(payment.NewPostgresStore(pool)), svc)
	result, err := bridge.FulfillVerifiedCallback(ctx, payment.VerifiedCallback{
		TenantID:        tenantID,
		Provider:        "mock",
		ExternalTradeNo: order.TradeNo,
		ProviderEventID: "evt-sub-link",
		PaidAmount:      decimal.RequireFromString("50.00000000"),
		CurrencyCode:    "USD",
		Timestamp:       time.Date(2026, 6, 2, 10, 5, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("FulfillVerifiedCallback: %v", err)
	}
	if !result.Completed || result.Idempotent {
		t.Fatalf("callback result=%+v want first completed non-idempotent", result)
	}

	link := readSubscriptionRechargeLink(t, ctx, pool, tenantID, order.ID)
	if link.SubscriptionStatus != string(StatusActive) {
		t.Fatalf("subscription status=%q want active", link.SubscriptionStatus)
	}
	if link.SubscriptionOrderStatus != string(OrderStatusActive) {
		t.Fatalf("subscription order status=%q want active", link.SubscriptionOrderStatus)
	}
	if link.SubscriptionOrderRechargeID == 0 || link.SubscriptionOrderRechargeID != link.RechargeOrderID ||
		link.UserSubscriptionOrderID != order.ID || link.BillingEventRechargeID != link.RechargeOrderID {
		t.Fatalf("recharge linkage mismatch: %+v; mutation dropping subscription_orders.recharge_order_id or billing_events link must fail this test", link)
	}
	assertSubscriptionBalance(t, ctx, pool, tenantID, userID, "50.00000000")
}

func TestPaymentBridgeDuplicateCallbackIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openSubscriptionPool(t, ctx)
	tenantID, userID := seedSubscriptionUser(t, ctx, pool, "callback-idem")

	svc := newSubscriptionIntegrationService(pool, "callback-idem", time.Date(2026, 6, 2, 11, 0, 0, 0, time.UTC))
	plan := createIntegrationPlan(t, ctx, svc, tenantID, "idem-plan", decimal.RequireFromString("75.00000000"), DurationDay, 30)
	order := createIntegrationOrder(t, ctx, svc, tenantID, userID, plan.ID, "mock")
	bridge := NewPaymentBridge(payment.NewService(payment.NewPostgresStore(pool)), svc)
	cb := payment.VerifiedCallback{
		TenantID:        tenantID,
		Provider:        "mock",
		ExternalTradeNo: order.TradeNo,
		ProviderEventID: "evt-sub-idem",
		PaidAmount:      decimal.RequireFromString("75.00000000"),
		CurrencyCode:    "USD",
		Timestamp:       time.Date(2026, 6, 2, 11, 5, 0, 0, time.UTC),
	}
	if _, err := bridge.FulfillVerifiedCallback(ctx, cb); err != nil {
		t.Fatalf("first callback: %v", err)
	}
	replay, err := bridge.FulfillVerifiedCallback(ctx, cb)
	if err != nil {
		t.Fatalf("replay callback: %v", err)
	}
	if !replay.Idempotent || !replay.Completed {
		t.Fatalf("replay=%+v want idempotent completed", replay)
	}

	assertSubscriptionCount(t, ctx, pool, tenantID, order.ID, 1)
	assertSubscriptionOrderStatus(t, ctx, pool, tenantID, order.ID, string(OrderStatusActive))
	assertSubscriptionBalance(t, ctx, pool, tenantID, userID, "75.00000000")
	assertSubscriptionBillingEventCount(t, ctx, pool, tenantID, order.RechargeOrderID, 1)
}

func TestExpireDueSubscriptionsMarksExpired(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openSubscriptionPool(t, ctx)
	tenantID, userID := seedSubscriptionUser(t, ctx, pool, "expire")

	start := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	svc := newSubscriptionIntegrationService(pool, "expire", start)
	plan := createIntegrationPlan(t, ctx, svc, tenantID, "expire-plan", decimal.RequireFromString("10.00000000"), DurationHour, 1)
	order := createIntegrationOrder(t, ctx, svc, tenantID, userID, plan.ID, "mock")
	bridge := NewPaymentBridge(payment.NewService(payment.NewPostgresStore(pool)), svc)
	if _, err := bridge.FulfillVerifiedCallback(ctx, payment.VerifiedCallback{
		TenantID:        tenantID,
		Provider:        "mock",
		ExternalTradeNo: order.TradeNo,
		ProviderEventID: "evt-sub-expire",
		PaidAmount:      decimal.RequireFromString("10.00000000"),
		CurrencyCode:    "USD",
		Timestamp:       start,
	}); err != nil {
		t.Fatalf("activate expiring subscription: %v", err)
	}

	expired, err := svc.ExpireDueSubscriptions(ctx, ExpireDueInput{
		TenantID: tenantID,
		Now:      start.Add(2 * time.Hour),
		Limit:    25,
	})
	if err != nil {
		t.Fatalf("ExpireDueSubscriptions: %v", err)
	}
	if expired != 1 {
		t.Fatalf("expired=%d want 1", expired)
	}
	assertSubscriptionStatus(t, ctx, pool, tenantID, order.ID, string(StatusExpired))
	active, err := svc.ListUserSubscriptions(ctx, ListUserSubscriptionsInput{TenantID: tenantID, UserID: userID, ActiveOnly: true})
	if err != nil {
		t.Fatalf("ListUserSubscriptions active: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("active subscriptions after expiry=%d want 0", len(active))
	}
}

func TestCreateOrderEnforcesPurchaseCapAgainstPendingOrders(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openSubscriptionPool(t, ctx)
	tenantID, userID := seedSubscriptionUser(t, ctx, pool, "pending-cap")

	svc := newSubscriptionIntegrationService(pool, "pending-cap", time.Date(2026, 6, 2, 13, 0, 0, 0, time.UTC))
	plan := createIntegrationPlan(t, ctx, svc, tenantID, "pending-cap-plan", decimal.RequireFromString("12.00000000"), DurationDay, 30)
	if _, err := pool.Exec(ctx,
		`UPDATE subscription_plans SET max_purchases_per_user=1 WHERE tenant_id=$1 AND id=$2`,
		tenantID, plan.ID,
	); err != nil {
		t.Fatalf("tighten purchase cap: %v", err)
	}
	if _, err := svc.CreateOrder(ctx, CreateOrderInput{
		TenantID: tenantID,
		UserID:   userID,
		PlanID:   plan.ID,
		Provider: "mock",
	}); err != nil {
		t.Fatalf("first CreateOrder: %v", err)
	}
	_, err := svc.CreateOrder(ctx, CreateOrderInput{
		TenantID: tenantID,
		UserID:   userID,
		PlanID:   plan.ID,
		Provider: "mock",
	})
	if !errors.Is(err, ErrPurchaseLimit) {
		t.Fatalf("second CreateOrder err=%v want ErrPurchaseLimit because pending order counts against cap", err)
	}
	assertPendingRechargeCount(t, ctx, pool, tenantID, userID, 1)
}

func TestUpdatePlanDuplicateCodeReturnsConflict(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openSubscriptionPool(t, ctx)
	tenantID, _ := seedSubscriptionUser(t, ctx, pool, "plan-conflict")

	svc := newSubscriptionIntegrationService(pool, "plan-conflict", time.Date(2026, 6, 2, 14, 0, 0, 0, time.UTC))
	first := createIntegrationPlan(t, ctx, svc, tenantID, "plan-conflict-a", decimal.RequireFromString("12.00000000"), DurationDay, 30)
	second := createIntegrationPlan(t, ctx, svc, tenantID, "plan-conflict-b", decimal.RequireFromString("13.00000000"), DurationDay, 30)

	code := first.Code
	_, err := svc.UpdatePlan(ctx, PlanPatch{TenantID: tenantID, ID: second.ID, Code: &code})
	if !errors.Is(err, ErrPlanConflict) {
		t.Fatalf("UpdatePlan duplicate code err=%v want ErrPlanConflict", err)
	}
}

func openSubscriptionPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping subscription integration test")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedSubscriptionUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) (tenantID, userID int64) {
	t.Helper()
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		fmt.Sprintf("subscription-%s-%d", suffix, time.Now().UnixNano()),
	).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		tenantID, "subscription-user-"+suffix,
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM payment_audit_log WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM billing_events WHERE tenant_id=$1 AND recharge_order_id IS NOT NULL`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM user_balances WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM user_subscriptions WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM subscription_orders WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM subscription_plans WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM recharge_orders WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})
	return tenantID, userID
}

func newSubscriptionIntegrationService(pool *pgxpool.Pool, suffix string, now time.Time) *Service {
	paymentSvc := payment.NewService(payment.NewPostgresStore(pool))
	counter := 0
	return NewService(NewPostgresStore(pool), paymentSvc,
		WithClock(func() time.Time { return now }),
		WithTradeSuffixGenerator(func(context.Context) (string, error) {
			counter++
			return fmt.Sprintf("%s-%d-%d", suffix, now.UnixNano(), counter), nil
		}),
	)
}

func createIntegrationPlan(t *testing.T, ctx context.Context, svc *Service, tenantID int64, name string, price decimal.Decimal, unit DurationUnit, value int) Plan {
	t.Helper()
	plan, err := svc.CreatePlan(ctx, PlanInput{
		TenantID:            tenantID,
		Code:                name,
		Name:                "Subscription " + name,
		Description:         "integration fixture",
		Price:               price,
		CurrencyCode:        "USD",
		DurationUnit:        unit,
		DurationValue:       value,
		QuotaLimit:          1000,
		QuotaResetPeriod:    ResetMonthly,
		MaxPurchasesPerUser: 5,
		Enabled:             true,
		SortOrder:           10,
	})
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	return plan
}

func createIntegrationOrder(t *testing.T, ctx context.Context, svc *Service, tenantID, userID, planID int64, provider string) Order {
	t.Helper()
	order, err := svc.CreateOrder(ctx, CreateOrderInput{
		TenantID: tenantID,
		UserID:   userID,
		PlanID:   planID,
		Provider: provider,
		Now:      time.Date(2026, 6, 2, 10, 1, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if order.RechargeOrderID == 0 || order.TradeNo == "" {
		t.Fatalf("CreateOrder returned missing recharge link: %+v", order)
	}
	return order
}

type subscriptionRechargeLink struct {
	SubscriptionOrderStatus     string
	SubscriptionStatus          string
	SubscriptionOrderRechargeID int64
	RechargeOrderID             int64
	UserSubscriptionOrderID     int64
	BillingEventRechargeID      int64
}

func readSubscriptionRechargeLink(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, orderID int64) subscriptionRechargeLink {
	t.Helper()
	var link subscriptionRechargeLink
	if err := pool.QueryRow(ctx, `
SELECT so.status, us.status, so.recharge_order_id, ro.id, us.source_order_id, be.recharge_order_id
FROM subscription_orders so
JOIN recharge_orders ro ON ro.tenant_id=so.tenant_id AND ro.id=so.recharge_order_id
JOIN user_subscriptions us ON us.tenant_id=so.tenant_id AND us.source_order_id=so.id
JOIN billing_events be ON be.tenant_id=so.tenant_id
  AND be.recharge_order_id=so.recharge_order_id
  AND be.event_type='balance_recharged'
WHERE so.tenant_id=$1 AND so.id=$2`,
		tenantID, orderID,
	).Scan(&link.SubscriptionOrderStatus, &link.SubscriptionStatus, &link.SubscriptionOrderRechargeID,
		&link.RechargeOrderID, &link.UserSubscriptionOrderID, &link.BillingEventRechargeID); err != nil {
		t.Fatalf("read subscription recharge link: %v", err)
	}
	return link
}

func assertSubscriptionBalance(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID int64, want string) {
	t.Helper()
	var balance string
	if err := pool.QueryRow(ctx,
		`SELECT balance::text FROM user_balances WHERE tenant_id=$1 AND user_id=$2`,
		tenantID, userID,
	).Scan(&balance); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	if balance != want {
		t.Fatalf("balance=%q want %q", balance, want)
	}
}

func assertSubscriptionCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, orderID int64, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM user_subscriptions WHERE tenant_id=$1 AND source_order_id=$2`,
		tenantID, orderID,
	).Scan(&count); err != nil {
		t.Fatalf("count subscriptions: %v", err)
	}
	if count != want {
		t.Fatalf("subscription count=%d want %d", count, want)
	}
}

func assertSubscriptionOrderStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, orderID int64, want string) {
	t.Helper()
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM subscription_orders WHERE tenant_id=$1 AND id=$2`,
		tenantID, orderID,
	).Scan(&status); err != nil {
		t.Fatalf("read order status: %v", err)
	}
	if status != want {
		t.Fatalf("order status=%q want %q", status, want)
	}
}

func assertSubscriptionStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, orderID int64, want string) {
	t.Helper()
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM user_subscriptions WHERE tenant_id=$1 AND source_order_id=$2`,
		tenantID, orderID,
	).Scan(&status); err != nil {
		t.Fatalf("read subscription status: %v", err)
	}
	if status != want {
		t.Fatalf("subscription status=%q want %q", status, want)
	}
}

func assertSubscriptionBillingEventCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, rechargeOrderID int64, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND recharge_order_id=$2 AND event_type='balance_recharged'`,
		tenantID, rechargeOrderID,
	).Scan(&count); err != nil {
		t.Fatalf("count billing recharge events: %v", err)
	}
	if count != want {
		t.Fatalf("billing recharge events=%d want %d", count, want)
	}
}

func assertPendingRechargeCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID int64, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM recharge_orders WHERE tenant_id=$1 AND user_id=$2 AND status='PENDING'`,
		tenantID, userID,
	).Scan(&count); err != nil {
		t.Fatalf("count pending recharge orders: %v", err)
	}
	if count != want {
		t.Fatalf("pending recharge orders=%d want %d; subscription order rejection must compensate orphan recharge rows", count, want)
	}
}
