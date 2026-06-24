//go:build integration_pg

package alerting

import (
	"context"
	"os"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

// Against a real Postgres the advisory lock is mutually exclusive: while one
// holder owns it a second TryAcquire returns false, and after release the lock
// becomes available again. This is the property that dedups alerts across
// replicas. Mutation guard: if TryAcquire ignored the pg_try_advisory_lock
// result and always returned true, the held-case assertion goes red.
func TestPostgresLeaderLock_MutualExclusionPG(t *testing.T) {
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer pool.Close()
	lock := NewPostgresLeaderLock(pool)

	got1, release1, err := lock.TryAcquire(ctx)
	if err != nil || !got1 {
		t.Fatalf("first acquire must win, got=%v err=%v", got1, err)
	}
	got2, release2, err := lock.TryAcquire(ctx)
	if err != nil {
		t.Fatalf("second try errored: %v", err)
	}
	if got2 {
		if release2 != nil {
			release2()
		}
		release1()
		t.Fatal("second acquire must fail while the lock is held (would allow duplicate alerts)")
	}
	release1()

	got3, release3, err := lock.TryAcquire(ctx)
	if err != nil || !got3 {
		t.Fatalf("acquire after release must win, got=%v err=%v", got3, err)
	}
	release3()
}
