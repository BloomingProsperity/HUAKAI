// HUAKAI · iKun
//go:build integration_pg

package payment

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func openPaymentIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	// 40 conns: T2 用 32 goroutine 并发履约, 每个履约期间各持一条连接。
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn, MaxConns: 40})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type paymentFixture struct {
	t       *testing.T
	ctx     context.Context
	pool    *pgxpool.Pool
	suffix  string
	tenantA int64
	userA   int64
	tenantB int64
	userB   int64
}

func newPaymentFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *paymentFixture {
	t.Helper()
	f := &paymentFixture{t: t, ctx: ctx, pool: pool, suffix: uuid.NewString()}
	f.tenantA, f.userA = f.seedTenantUser("pay-a")
	f.tenantB, f.userB = f.seedTenantUser("pay-b")
	t.Cleanup(f.cleanup)
	return f
}

func (f *paymentFixture) seedTenantUser(label string) (int64, int64) {
	f.t.Helper()
	unique := label + "-" + f.suffix
	var tenantID int64
	if err := f.pool.QueryRow(f.ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, unique).Scan(&tenantID); err != nil {
		f.t.Fatalf("seed tenant %s: %v", label, err)
	}
	var userID int64
	if err := f.pool.QueryRow(f.ctx, `INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		tenantID, "user-"+unique).Scan(&userID); err != nil {
		f.t.Fatalf("seed user %s: %v", label, err)
	}
	return tenantID, userID
}

func (f *paymentFixture) cleanup() {
	ctx := context.Background()
	for _, tenantID := range []int64{f.tenantA, f.tenantB} {
		// FK 顺序: audit→orders, billing_events→payment_credits/payment_refunds, money facts→orders, orders→users。
		_, _ = f.pool.Exec(ctx, `DELETE FROM daily_checkin WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM payment_audit_events WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM payment_audit_log WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM billing_events WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM payment_refunds WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM payment_credits WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM payment_orders WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM user_balances WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM recharge_orders WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM users WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenantID)
	}
}

func (f *paymentFixture) countInt(query string, args ...any) int64 {
	f.t.Helper()
	var n int64
	if err := f.pool.QueryRow(f.ctx, query, args...).Scan(&n); err != nil {
		f.t.Fatalf("count query %q: %v", query, err)
	}
	return n
}
