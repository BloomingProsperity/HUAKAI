//go:build integration_pg

package apikeyexpiry

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	dbauth "github.com/BloomingProsperity/HUAKAI/internal/db/auth"
)

// TestSweepExpiredKeys guards AUTH-150: the sweeper may only materialize
// already-expired active keys as status='expired'. It must not touch future
// active keys or non-active keys whose expires_at is in the past.
//
// MUTATION: remove the expires_at <= NOW() upper bound from the SQL predicate;
// the future active key flips to expired and this test fails.
func TestSweepExpiredKeys(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openExpiryIntegrationPool(t, ctx)
	seed := seedExpiryFixture(t, ctx, pool)

	svc := NewService(dbauth.New(pool), WithBatchLimit(2))
	changed, err := svc.SweepExpiredKeys(ctx)
	if err != nil {
		t.Fatalf("SweepExpiredKeys: %v", err)
	}
	if changed != 1 {
		t.Fatalf("changed=%d want exactly 1 past active key", changed)
	}
	assertAPIKeyStatus(t, ctx, pool, seed.pastActiveID, "expired")
	assertAPIKeyStatus(t, ctx, pool, seed.futureActiveID, "active")
	assertAPIKeyStatus(t, ctx, pool, seed.pastRevokedID, "revoked")

	changed, err = svc.SweepExpiredKeys(ctx)
	if err != nil {
		t.Fatalf("second SweepExpiredKeys: %v", err)
	}
	if changed != 0 {
		t.Fatalf("second changed=%d want idempotent 0", changed)
	}
}

type expiryFixture struct {
	tenantID       int64
	userID         int64
	pastActiveID   int64
	futureActiveID int64
	pastRevokedID  int64
}

func openExpiryIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedExpiryFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) expiryFixture {
	t.Helper()
	suffix := uuid.NewString()
	now := time.Now().UTC()
	f := expiryFixture{}
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"apikey-expiry-"+suffix,
	).Scan(&f.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		f.tenantID, "user-"+suffix,
	).Scan(&f.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	f.pastActiveID = insertExpiryAPIKey(t, ctx, pool, f.tenantID, f.userID, suffix+"-past-active", "active", now.Add(-time.Minute))
	f.futureActiveID = insertExpiryAPIKey(t, ctx, pool, f.tenantID, f.userID, suffix+"-future-active", "active", now.Add(time.Hour))
	f.pastRevokedID = insertExpiryAPIKey(t, ctx, pool, f.tenantID, f.userID, suffix+"-past-revoked", "revoked", now.Add(-time.Minute))

	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM api_keys WHERE tenant_id=$1`, f.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id=$1`, f.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, f.tenantID)
	})
	return f
}

func insertExpiryAPIKey(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID int64, name, status string, expiresAt time.Time) int64 {
	t.Helper()
	plaintext := "hk_test_" + uuid.NewString()
	prefix := plaintext[:16]
	var id int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		tenantID, userID, name, "bcrypt-not-used-by-expiry-test", prefix, status, expiresAt,
	).Scan(&id); err != nil {
		t.Fatalf("seed api_key %s: %v", name, err)
	}
	return id
}

func assertAPIKeyStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64, want string) {
	t.Helper()
	var got string
	if err := pool.QueryRow(ctx, `SELECT status FROM api_keys WHERE id=$1`, id).Scan(&got); err != nil {
		t.Fatalf("read status for key %d: %v", id, err)
	}
	if got != want {
		t.Fatalf("api_key %d status=%s want %s", id, got, want)
	}
}
