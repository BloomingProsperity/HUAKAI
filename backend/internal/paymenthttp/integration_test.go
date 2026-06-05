//go:build integration_pg

package paymenthttp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

func TestPaymentHTTPWebhookRejectsForgedSignatureBeforeCredit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentHTTPPool(t, ctx)
	tenantID, userID := seedPaymentHTTPUser(t, ctx, pool, "bad-signature")
	svc := payment.NewService(payment.NewPostgresStore(pool))
	order := openPaymentHTTPOrder(t, ctx, svc, tenantID, userID, externalTradeNoForTenant(tenantID, "bad-signature"), "hmacpay")
	mux := paymentHTTPIntegrationRouter(t, svc, time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC))
	raw := webhookPayload("hmacpay", order.OutTradeNo, "evt_bad_signature", "50.00000000", "USD")
	req := httptest.NewRequest(http.MethodPost, "/v1/payment/webhooks/hmacpay", strings.NewReader(string(raw)))
	req.Header = signedHeaders(time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC), raw, "wrong-secret")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad signature status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	assertPaymentHTTPNoUserBalance(t, ctx, pool, tenantID, userID)
	assertPaymentHTTPBalanceEventCount(t, ctx, pool, tenantID, order.ID, 0)
	assertPaymentHTTPOrderStatus(t, ctx, pool, tenantID, order.ID, payment.StatusPending)
}

func TestPaymentHTTPWebhookReplayCompletedIsIdempotentAndDoesNotDoubleCredit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentHTTPPool(t, ctx)
	tenantID, userID := seedPaymentHTTPUser(t, ctx, pool, "replay")
	svc := payment.NewService(payment.NewPostgresStore(pool))
	order := openPaymentHTTPOrder(t, ctx, svc, tenantID, userID, externalTradeNoForTenant(tenantID, "replay"), "hmacpay")
	now := time.Date(2026, 6, 2, 10, 5, 0, 0, time.UTC)
	mux := paymentHTTPIntegrationRouter(t, svc, now)
	raw := webhookPayload("hmacpay", order.OutTradeNo, "evt_replay", "50.00000000", "USD")

	first := signedWebhookRequest(now, raw, "secret-one")
	firstRec := httptest.NewRecorder()
	mux.ServeHTTP(firstRec, first)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("first callback status=%d body=%s want 200", firstRec.Code, firstRec.Body.String())
	}
	assertPaymentHTTPUserBalance(t, ctx, pool, tenantID, userID, "50.00000000")
	assertPaymentHTTPBalanceEventCount(t, ctx, pool, tenantID, order.ID, 1)

	replay := signedWebhookRequest(now, raw, "secret-one")
	replayRec := httptest.NewRecorder()
	mux.ServeHTTP(replayRec, replay)
	if replayRec.Code != http.StatusOK {
		t.Fatalf("replay status=%d body=%s want 200", replayRec.Code, replayRec.Body.String())
	}
	if !strings.Contains(replayRec.Body.String(), `"idempotent":true`) {
		t.Fatalf("replay body=%s must expose idempotent=true", replayRec.Body.String())
	}
	assertPaymentHTTPUserBalance(t, ctx, pool, tenantID, userID, "50.00000000")
	assertPaymentHTTPBalanceEventCount(t, ctx, pool, tenantID, order.ID, 1)
}

func TestPaymentHTTPWebhookAmountCurrencyProviderMismatchDoesNotCredit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentHTTPPool(t, ctx)
	tenantID, userID := seedPaymentHTTPUser(t, ctx, pool, "mismatch")
	svc := payment.NewService(payment.NewPostgresStore(pool))
	now := time.Date(2026, 6, 2, 10, 10, 0, 0, time.UTC)
	mux := paymentHTTPIntegrationRouter(t, svc, now)

	cases := []struct {
		name          string
		routeProvider string
		routeSecret   string
		bodyProvider  string
		bodyAmount    string
		bodyCurrency  string
		wantReason    string
		orderProvider string
	}{
		{name: "amount", routeProvider: "hmacpay", routeSecret: "secret-one", bodyProvider: "hmacpay", bodyAmount: "5.00000000", bodyCurrency: "USD", wantReason: payment.AuditReasonAmountMismatch, orderProvider: "hmacpay"},
		{name: "currency", routeProvider: "hmacpay", routeSecret: "secret-one", bodyProvider: "hmacpay", bodyAmount: "50.00000000", bodyCurrency: "EUR", wantReason: payment.AuditReasonAmountMismatch, orderProvider: "hmacpay"},
		{name: "provider", routeProvider: "otherpay", routeSecret: "secret-two", bodyProvider: "otherpay", bodyAmount: "50.00000000", bodyCurrency: "USD", wantReason: payment.AuditReasonProviderMismatch, orderProvider: "hmacpay"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			order := openPaymentHTTPOrder(t, ctx, svc, tenantID, userID, externalTradeNoForTenant(tenantID, "mismatch-"+tc.name), tc.orderProvider)
			raw := webhookPayload(tc.bodyProvider, order.OutTradeNo, "evt_mismatch_"+tc.name, tc.bodyAmount, tc.bodyCurrency)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, signedWebhookRequestForProvider(tc.routeProvider, now, raw, tc.routeSecret))
			if rec.Code != http.StatusOK {
				t.Fatalf("mismatch status=%d body=%s want 200", rec.Code, rec.Body.String())
			}
			assertPaymentHTTPNoUserBalance(t, ctx, pool, tenantID, userID)
			assertPaymentHTTPBalanceEventCount(t, ctx, pool, tenantID, order.ID, 0)
			assertPaymentHTTPOrderStatus(t, ctx, pool, tenantID, order.ID, payment.StatusPending)
			assertPaymentHTTPAuditReasonCount(t, ctx, pool, tenantID, order.ID, tc.wantReason, 1)
		})
	}
}

func TestPaymentHTTPWebhookDerivesTenantFromExternalTradeNoNotBody(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openPaymentHTTPPool(t, ctx)
	tenantA, userA := seedPaymentHTTPUser(t, ctx, pool, "tenant-a")
	tenantB, userB := seedPaymentHTTPUser(t, ctx, pool, "tenant-b")
	svc := payment.NewService(payment.NewPostgresStore(pool))

	sharedTradeNo := externalTradeNoForTenant(tenantA, "shared")
	orderA := openPaymentHTTPOrder(t, ctx, svc, tenantA, userA, sharedTradeNo, "hmacpay")
	orderB := openPaymentHTTPOrder(t, ctx, svc, tenantB, userB, sharedTradeNo, "hmacpay")
	now := time.Date(2026, 6, 2, 10, 20, 0, 0, time.UTC)
	mux := paymentHTTPIntegrationRouter(t, svc, now)
	raw := []byte(fmt.Sprintf(
		`{"provider":"hmacpay","tenant_id":%d,"external_trade_no":"%s","provider_event_id":"evt_cross_tenant","amount":"50.00000000","currency":"USD"}`,
		tenantB, sharedTradeNo,
	))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, signedWebhookRequest(now, raw, "secret-one"))

	if rec.Code != http.StatusOK {
		t.Fatalf("cross-tenant webhook status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	assertPaymentHTTPUserBalance(t, ctx, pool, tenantA, userA, "50.00000000")
	assertPaymentHTTPNoUserBalance(t, ctx, pool, tenantB, userB)
	assertPaymentHTTPBalanceEventCount(t, ctx, pool, tenantA, orderA.ID, 1)
	assertPaymentHTTPBalanceEventCount(t, ctx, pool, tenantB, orderB.ID, 0)
	assertPaymentHTTPOrderStatus(t, ctx, pool, tenantA, orderA.ID, payment.StatusCompleted)
	assertPaymentHTTPOrderStatus(t, ctx, pool, tenantB, orderB.ID, payment.StatusPending)
}

func openPaymentHTTPPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration test")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedPaymentHTTPUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) (tenantID, userID int64) {
	t.Helper()
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		fmt.Sprintf("paymenthttp-%s-%d", suffix, time.Now().UnixNano()),
	).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		tenantID, "paymenthttp-user-"+suffix,
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM payment_audit_events WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM payment_audit_log WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM billing_events WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM payment_refund_requests WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM payment_credits WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM payment_orders WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM user_balances WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM recharge_orders WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})
	return tenantID, userID
}

func openPaymentHTTPOrder(t *testing.T, ctx context.Context, svc *payment.Service, tenantID, userID int64, tradeNo, provider string) payment.Order {
	t.Helper()
	res, err := svc.OpenRecharge(ctx, payment.OpenInput{
		TenantID:          tenantID,
		UserID:            userID,
		ExternalTradeNo:   tradeNo,
		Provider:          provider,
		Amount:            decimal.RequireFromString("50.00000000"),
		CurrencyCode:      "USD",
		MaxPendingPerUser: 10,
		DailyAmountLimit:  decimal.RequireFromString("500.00000000"),
		Now:               time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("OpenRecharge integration order: %v", err)
	}
	return res.Order
}

func paymentHTTPIntegrationRouter(t *testing.T, svc *payment.Service, now time.Time) *chi.Mux {
	t.Helper()
	r := chi.NewRouter()
	MountRoutes(r, Deps{
		Service: svc,
		Providers: map[string]ProviderBinding{
			"hmacpay": {
				Provider: NewHMACProvider(WithClock(func() time.Time { return now })),
				Secret:   "secret-one",
			},
			"otherpay": {
				Provider: NewHMACProvider(WithClock(func() time.Time { return now })),
				Secret:   "secret-two",
			},
		},
		Clock: func() time.Time { return now },
	})
	return r
}

func webhookPayload(provider, tradeNo, eventID, amount, currency string) []byte {
	return []byte(fmt.Sprintf(
		`{"provider":"%s","external_trade_no":"%s","provider_event_id":"%s","amount":"%s","currency":"%s"}`,
		provider, tradeNo, eventID, amount, currency,
	))
}

func signedWebhookRequest(now time.Time, raw []byte, secret string) *http.Request {
	return signedWebhookRequestForProvider("hmacpay", now, raw, secret)
}

func signedWebhookRequestForProvider(provider string, now time.Time, raw []byte, secret string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/payment/webhooks/"+provider, strings.NewReader(string(raw)))
	req.Header = signedHeaders(now, raw, secret)
	return req
}

func assertPaymentHTTPOrderStatus(t *testing.T, ctx context.Context, pool queryPool, tenantID, orderID int64, want payment.OrderStatus) {
	t.Helper()
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM payment_orders WHERE tenant_id=$1 AND id=$2`,
		tenantID, orderID,
	).Scan(&status); err != nil {
		t.Fatalf("read payment order status: %v", err)
	}
	if payment.OrderStatus(status) != want {
		t.Fatalf("order status=%q want %q", status, want)
	}
}

func assertPaymentHTTPUserBalance(t *testing.T, ctx context.Context, pool queryPool, tenantID, userID int64, want string) {
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

func assertPaymentHTTPNoUserBalance(t *testing.T, ctx context.Context, pool queryPool, tenantID, userID int64) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM user_balances WHERE tenant_id=$1 AND user_id=$2)`,
		tenantID, userID,
	).Scan(&exists); err != nil {
		t.Fatalf("read user balance existence: %v", err)
	}
	if exists {
		t.Fatalf("user balance exists for tenant=%d user=%d; rejected callback must not credit", tenantID, userID)
	}
}

func assertPaymentHTTPBalanceEventCount(t *testing.T, ctx context.Context, pool queryPool, tenantID, orderID int64, want int) {
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

func assertPaymentHTTPAuditReasonCount(t *testing.T, ctx context.Context, pool queryPool, tenantID, orderID int64, reason string, want int) {
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
