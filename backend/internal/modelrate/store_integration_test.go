//go:build integration_pg

package modelrate

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

func TestStoreUpsertDeleteVerifyAndDoesNotRewriteUsage(t *testing.T) {
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	resetModelRateState(t, ctx, pool)

	var usageBefore int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM usage_records`).Scan(&usageBefore); err != nil {
		t.Fatalf("count usage_records: %v", err)
	}

	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	store := NewPostgresStore(pool, signer)
	input := decimal.RequireFromString("2.5")
	row, err := store.Upsert(ctx, UpsertParams{
		Vendor: "openai", Model: "gpt-5.6",
		Rates:     RateBuckets{Input: &input},
		Actor:     "admin_token:317",
		ActorRole: "platform_admin",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if row.Vendor != "openai" || row.Rates.Input == nil || row.Rates.Input.String() != "2.5" {
		t.Fatalf("row=%+v", row)
	}
	if _, err := store.Get(ctx, "openai", "gpt-5.6"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	both := decimal.RequireFromString("9")
	if _, err := store.Upsert(ctx, UpsertParams{
		Vendor: "openai", Model: "gpt-5.6",
		Rates:     RateBuckets{Input: &input, Output: &both},
		Actor:     "admin_token:317",
		ActorRole: "platform_admin",
	}); err != nil {
		t.Fatalf("Upsert both: %v", err)
	}
	replaced, err := store.Upsert(ctx, UpsertParams{
		Vendor: "openai", Model: "gpt-5.6",
		Rates:     RateBuckets{Input: &input},
		Actor:     "admin_token:317",
		ActorRole: "platform_admin",
	})
	if err != nil {
		t.Fatalf("Upsert replace: %v", err)
	}
	if replaced.Rates.Input == nil || replaced.Rates.Input.String() != "2.5" || replaced.Rates.Output != nil {
		t.Fatalf("replace must keep only submitted buckets: %+v", replaced.Rates)
	}
	listed, err := store.List(ctx)
	if err != nil || len(listed) != 1 {
		t.Fatalf("List=%v err=%v", listed, err)
	}
	clean, err := store.VerifyChain(ctx)
	if err != nil || !clean.OK {
		t.Fatalf("VerifyChain clean=%+v err=%v", clean, err)
	}
	if err := store.Delete(ctx, DeleteParams{
		Vendor: "openai", Model: "gpt-5.6",
		Actor: "admin_token:317", ActorRole: "platform_admin",
	}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(ctx, "openai", "gpt-5.6"); err != ErrNotFound {
		t.Fatalf("Get after delete err=%v want not found", err)
	}

	var usageAfter int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM usage_records`).Scan(&usageAfter); err != nil {
		t.Fatalf("count usage_records after: %v", err)
	}
	if usageAfter != usageBefore {
		t.Fatalf("usage_records changed %d -> %d; override write must not rewrite settled bills", usageBefore, usageAfter)
	}

	var tamperedID int64
	if err := pool.QueryRow(ctx, `
		UPDATE model_rate_override_audit_log
		   SET entry_hash = decode(repeat('00', 32), 'hex')
		 WHERE id = (SELECT max(id) FROM model_rate_override_audit_log)
		 RETURNING id`).Scan(&tamperedID); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	broken, err := store.VerifyChain(ctx)
	if err != nil {
		t.Fatalf("VerifyChain tampered: %v", err)
	}
	if broken.OK || broken.RowID != tamperedID {
		t.Fatalf("tampered=%+v want fail on %d", broken, tamperedID)
	}
}

func TestStoreConcurrentUpsertSameKey(t *testing.T) {
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	resetModelRateState(t, ctx, pool)

	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	store := NewPostgresStore(pool, signer)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			value := decimal.NewFromInt(int64(n + 1))
			_, err := store.Upsert(ctx, UpsertParams{
				Vendor: "openai", Model: "gpt-concurrent",
				Rates:     RateBuckets{Input: &value},
				Actor:     "admin_token:317",
				ActorRole: "platform_admin",
			})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent upsert: %v", err)
		}
	}
	got, err := store.Get(ctx, "openai", "gpt-concurrent")
	if err != nil || got.Rates.Input == nil {
		t.Fatalf("Get after concurrent: %+v err=%v", got, err)
	}
	listed, err := store.List(ctx)
	if err != nil || len(listed) != 1 {
		t.Fatalf("unique key broken: listed=%d err=%v", len(listed), err)
	}
	proof, err := store.VerifyChain(ctx)
	if err != nil || !proof.OK {
		t.Fatalf("chain after concurrent upserts: %+v err=%v", proof, err)
	}
}

func resetModelRateState(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `TRUNCATE model_rate_override_audit_log, model_rate_overrides RESTART IDENTITY`); err != nil {
		t.Fatalf("truncate model rate tables: %v", err)
	}
}
