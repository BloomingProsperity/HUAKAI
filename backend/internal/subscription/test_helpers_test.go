// HUAKAI · iKun
//go:build integration_pg

package subscription

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func openIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn, MaxConns: 40})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type subFixture struct {
	t       *testing.T
	ctx     context.Context
	pool    *pgxpool.Pool
	suffix  string
	tenantA int64
	userA   int64
	userA2  int64
	tenantB int64
	userB   int64
}

func newSubFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *subFixture {
	t.Helper()
	f := &subFixture{t: t, ctx: ctx, pool: pool, suffix: uuid.NewString()}
	f.tenantA = f.seedTenant("sub-a")
	f.userA = f.seedUser(f.tenantA, "a1")
	f.userA2 = f.seedUser(f.tenantA, "a2")
	f.tenantB = f.seedTenant("sub-b")
	f.userB = f.seedUser(f.tenantB, "b1")
	t.Cleanup(f.cleanup)
	return f
}

func (f *subFixture) seedTenant(label string) int64 {
	f.t.Helper()
	var id int64
	if err := f.pool.QueryRow(f.ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, label+"-"+f.suffix).Scan(&id); err != nil {
		f.t.Fatalf("seed tenant %s: %v", label, err)
	}
	return id
}

func (f *subFixture) seedUser(tenantID int64, label string) int64 {
	f.t.Helper()
	var id int64
	if err := f.pool.QueryRow(f.ctx, `INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		tenantID, "user-"+label+"-"+f.suffix).Scan(&id); err != nil {
		f.t.Fatalf("seed user %s: %v", label, err)
	}
	return id
}

func (f *subFixture) cleanup() {
	ctx := context.Background()
	for _, tenantID := range []int64{f.tenantA, f.tenantB} {
		_, _ = f.pool.Exec(ctx, `DELETE FROM subscription_expiry_reminders WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM subscription_audit_events WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM subscription_policy_links WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM user_subscriptions WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM subscription_plans WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM quota_policies WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM users WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenantID)
	}
}

func (f *subFixture) setUserEmail(tenantID, userID int64, email string) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx, `UPDATE users SET email=$3 WHERE tenant_id=$1 AND id=$2`, tenantID, userID, email); err != nil {
		f.t.Fatalf("set user email: %v", err)
	}
}

func (f *subFixture) userGroup(tenantID, userID int64) string {
	f.t.Helper()
	var g string
	if err := f.pool.QueryRow(f.ctx, `SELECT user_group FROM users WHERE tenant_id=$1 AND id=$2`, tenantID, userID).Scan(&g); err != nil {
		f.t.Fatalf("read user_group: %v", err)
	}
	return g
}

func (f *subFixture) countInt(query string, args ...any) int64 {
	f.t.Helper()
	var n int64
	if err := f.pool.QueryRow(f.ctx, query, args...).Scan(&n); err != nil {
		f.t.Fatalf("count query %q: %v", query, err)
	}
	return n
}

func dec(s string) *decimal.Decimal {
	d := decimal.RequireFromString(s)
	return &d
}
