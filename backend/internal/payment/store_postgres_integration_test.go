//go:build integration_pg

package payment

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func TestPostgresStoreOpenRechargePersistsPendingOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPaymentPool(t, ctx)
	tenantID, userID := seedPaymentUser(t, ctx, pool, "create")

	svc := NewService(NewPostgresStore(pool), WithExternalTradeNoGenerator(fixedExternalTradeNo("trade-create-50")))
	amount := decimal.RequireFromString("50.00000000")
	res, err := svc.OpenRecharge(ctx, OpenInput{
		TenantID:          tenantID,
		UserID:            userID,
		Amount:            amount,
		CurrencyCode:      "usd",
		MaxPendingPerUser: 3,
		DailyAmountLimit:  decimal.RequireFromString("500.00000000"),
		Now:               time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("OpenRecharge: %v", err)
	}
	if res.Order.Status != StatusPending {
		t.Fatalf("status=%q want %q", res.Order.Status, StatusPending)
	}
	if res.Order.ExternalTradeNo != "trade-create-50" {
		t.Fatalf("external trade no=%q want generated value", res.Order.ExternalTradeNo)
	}
	if !res.Order.CreditedAmount.Equal(amount) {
		t.Fatalf("credited amount=%s want %s", res.Order.CreditedAmount, amount)
	}
	if res.Order.RechargeRef == "" {
		t.Fatal("recharge ref must be populated")
	}

	var status, tradeNo, creditedText, currency, ref string
	if err := pool.QueryRow(ctx, `
SELECT status, external_trade_no, credited_amount::text, currency_code, recharge_ref
FROM recharge_orders
WHERE tenant_id=$1 AND id=$2`, tenantID, res.Order.ID).Scan(&status, &tradeNo, &creditedText, &currency, &ref); err != nil {
		t.Fatalf("read recharge order: %v", err)
	}
	if status != string(StatusPending) {
		t.Fatalf("row status=%q want PENDING", status)
	}
	if tradeNo != "trade-create-50" {
		t.Fatalf("row external_trade_no=%q want trade-create-50", tradeNo)
	}
	if creditedText != "50.00000000" {
		t.Fatalf("row credited_amount=%q want 50.00000000", creditedText)
	}
	if currency != "USD" {
		t.Fatalf("row currency=%q want USD", currency)
	}
	if ref != res.Order.RechargeRef {
		t.Fatalf("row recharge_ref=%q want result ref %q", ref, res.Order.RechargeRef)
	}
}

func TestPostgresStoreOpenRechargeEnforcesPendingLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPaymentPool(t, ctx)
	tenantID, userID := seedPaymentUser(t, ctx, pool, "pending-limit")

	svc := NewService(NewPostgresStore(pool), WithExternalTradeNoGenerator(sequenceExternalTradeNo("limit-trade")))
	input := OpenInput{
		TenantID:          tenantID,
		UserID:            userID,
		Amount:            decimal.RequireFromString("50.00000000"),
		CurrencyCode:      "USD",
		MaxPendingPerUser: 2,
		DailyAmountLimit:  decimal.RequireFromString("500.00000000"),
		Now:               time.Date(2026, 5, 31, 13, 0, 0, 0, time.UTC),
	}
	for i := 0; i < 2; i++ {
		if _, err := svc.OpenRecharge(ctx, input); err != nil {
			t.Fatalf("OpenRecharge #%d: %v", i+1, err)
		}
	}
	_, err := svc.OpenRecharge(ctx, input)
	if !errors.Is(err, ErrPendingLimit) {
		t.Fatalf("third pending order err=%v want ErrPendingLimit", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM recharge_orders
WHERE tenant_id=$1 AND user_id=$2 AND status='PENDING'`, tenantID, userID).Scan(&count); err != nil {
		t.Fatalf("count pending orders: %v", err)
	}
	if count != 2 {
		t.Fatalf("pending order count=%d want 2; mutation deleting guard would insert the third row", count)
	}
}

func TestPostgresStoreOpenRechargeEnforcesDailyAmountLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPaymentPool(t, ctx)
	tenantID, userID := seedPaymentUser(t, ctx, pool, "daily-limit")

	svc := NewService(NewPostgresStore(pool), WithExternalTradeNoGenerator(sequenceExternalTradeNo("daily-trade")))
	input := OpenInput{
		TenantID:          tenantID,
		UserID:            userID,
		Amount:            decimal.RequireFromString("50.00000000"),
		CurrencyCode:      "USD",
		MaxPendingPerUser: 10,
		DailyAmountLimit:  decimal.RequireFromString("99.00000000"),
		Now:               time.Date(2026, 5, 31, 15, 0, 0, 0, time.UTC),
	}
	if _, err := svc.OpenRecharge(ctx, input); err != nil {
		t.Fatalf("first OpenRecharge: %v", err)
	}
	_, err := svc.OpenRecharge(ctx, input)
	if !errors.Is(err, ErrDailyAmountLimit) {
		t.Fatalf("second same-day order err=%v want ErrDailyAmountLimit", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM recharge_orders
WHERE tenant_id=$1 AND user_id=$2`, tenantID, userID).Scan(&count); err != nil {
		t.Fatalf("count daily-limit orders: %v", err)
	}
	if count != 1 {
		t.Fatalf("daily-limit mutation should not insert the second row; count=%d want 1", count)
	}
}

func TestPostgresStoreOpenRechargeExternalTradeNoUniqueUnderRace(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPaymentPool(t, ctx)
	tenantID, userID := seedPaymentUser(t, ctx, pool, "trade-race")

	svc := NewService(NewPostgresStore(pool), WithExternalTradeNoGenerator(fixedExternalTradeNo("race-fixed-trade")))
	input := OpenInput{
		TenantID:          tenantID,
		UserID:            userID,
		Amount:            decimal.RequireFromString("50.00000000"),
		CurrencyCode:      "USD",
		MaxPendingPerUser: 10,
		DailyAmountLimit:  decimal.RequireFromString("500.00000000"),
		Now:               time.Date(2026, 5, 31, 14, 0, 0, 0, time.UTC),
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.OpenRecharge(ctx, input)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	var success, uniqueConflict int
	for err := range errs {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrExternalTradeConflict):
			uniqueConflict++
		default:
			t.Fatalf("unexpected concurrent OpenRecharge error: %v", err)
		}
	}
	if success != 1 || uniqueConflict != 1 {
		t.Fatalf("concurrent fixed trade no: success=%d uniqueConflict=%d, want exactly 1/1", success, uniqueConflict)
	}
}

func TestPostgresStoreOpenRechargeRequiresActiveTenantAndUser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPaymentPool(t, ctx)

	cases := []struct {
		name       string
		mutate     string
		tenantOnly bool
	}{
		{
			name:   "disabled user",
			mutate: `UPDATE users SET status='disabled' WHERE tenant_id=$1 AND id=$2`,
		},
		{
			name:   "deleted user status",
			mutate: `UPDATE users SET status='deleted' WHERE tenant_id=$1 AND id=$2`,
		},
		{
			name:   "soft-deleted user",
			mutate: `UPDATE users SET deleted_at=now() WHERE tenant_id=$1 AND id=$2`,
		},
		{
			name:       "disabled tenant",
			mutate:     `UPDATE tenants SET status='disabled' WHERE id=$1`,
			tenantOnly: true,
		},
		{
			name:       "soft-deleted tenant",
			mutate:     `UPDATE tenants SET deleted_at=now() WHERE id=$1`,
			tenantOnly: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenantID, userID := seedPaymentUser(t, ctx, pool, "inactive-"+tc.name)
			args := []any{tenantID, userID}
			if tc.tenantOnly {
				args = []any{tenantID}
			}
			if _, err := pool.Exec(ctx, tc.mutate, args...); err != nil {
				t.Fatalf("mutate fixture: %v", err)
			}

			svc := NewService(NewPostgresStore(pool), WithExternalTradeNoGenerator(sequenceExternalTradeNo("inactive-trade")))
			_, err := svc.OpenRecharge(ctx, OpenInput{
				TenantID:          tenantID,
				UserID:            userID,
				Amount:            decimal.RequireFromString("50.00000000"),
				CurrencyCode:      "USD",
				MaxPendingPerUser: 3,
				DailyAmountLimit:  decimal.RequireFromString("500.00000000"),
				Now:               time.Date(2026, 5, 31, 16, 0, 0, 0, time.UTC),
			})
			if !errors.Is(err, ErrAccountInactive) {
				t.Fatalf("OpenRecharge err=%v want ErrAccountInactive", err)
			}

			var count int
			if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM recharge_orders
WHERE tenant_id=$1 AND user_id=$2`, tenantID, userID).Scan(&count); err != nil {
				t.Fatalf("count recharge orders: %v", err)
			}
			if count != 0 {
				t.Fatalf("inactive account must not get an order; count=%d want 0", count)
			}
		})
	}
}

func TestLockUserAllowsDifferentUsersInSameTenant(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openPaymentPool(t, ctx)
	tenantID, firstUserID := seedPaymentUser(t, ctx, pool, "tenant-lock")
	secondUserID := seedAdditionalPaymentUser(t, ctx, pool, tenantID, "tenant-lock-b")

	firstTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin first tx: %v", err)
	}
	defer func() { _ = firstTx.Rollback(context.Background()) }()
	if err := lockUser(ctx, firstTx, tenantID, firstUserID); err != nil {
		t.Fatalf("first lockUser: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		lockCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		secondTx, err := pool.Begin(lockCtx)
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = secondTx.Rollback(context.Background()) }()
		done <- lockUser(lockCtx, secondTx, tenantID, secondUserID)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("second user's lockUser blocked or failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second user's lockUser blocked behind an unrelated user in the same tenant")
	}
}

func openPaymentPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
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

func seedPaymentUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) (tenantID, userID int64) {
	t.Helper()
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		fmt.Sprintf("payment-%s-%d", suffix, time.Now().UnixNano()),
	).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		tenantID, "payment-user-"+suffix,
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM recharge_orders WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})
	return tenantID, userID
}

func seedAdditionalPaymentUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, suffix string) int64 {
	t.Helper()
	var userID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		tenantID, "payment-user-"+suffix,
	).Scan(&userID); err != nil {
		t.Fatalf("seed additional user: %v", err)
	}
	return userID
}

func fixedExternalTradeNo(value string) ExternalTradeNoGenerator {
	return func(context.Context) (string, error) {
		return value, nil
	}
}

func sequenceExternalTradeNo(prefix string) ExternalTradeNoGenerator {
	var mu sync.Mutex
	var n int
	return func(context.Context) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		n++
		return fmt.Sprintf("%s-%02d", prefix, n), nil
	}
}
