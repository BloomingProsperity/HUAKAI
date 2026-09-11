//go:build integration_pg

package modelrate

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
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
	seed := seedSettledUsageRecord(t, ctx, pool)
	before := loadSettledUsageSnapshot(t, ctx, pool, seed.usageID)
	if before.actualCost != "1.23456789" || before.tokensInput != 321 || before.requestedModel != "gpt-settled-canary" {
		t.Fatalf("seed snapshot not canary: %+v", before)
	}
	t.Run("snapshot_catches_money_rewrite", func(t *testing.T) {
		mutated := before
		mutated.actualCost = "9.99999999"
		if settledUsageSnapshotsEqual(before, mutated) {
			t.Fatal("逐字段比较必须在 actual_cost 被改写时不相等;否则本测试会假绿")
		}
	})

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

	afterUpsertAndDelete := loadSettledUsageSnapshot(t, ctx, pool, seed.usageID)
	if !settledUsageSnapshotsEqual(before, afterUpsertAndDelete) {
		t.Fatalf("settled usage_records mutated after override write/delete\nbefore=%+v\nafter=%+v", before, afterUpsertAndDelete)
	}
	var tenantUsageCount int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM usage_records WHERE tenant_id=$1`, seed.tenantID).Scan(&tenantUsageCount); err != nil {
		t.Fatalf("count tenant usage_records: %v", err)
	}
	if tenantUsageCount != 1 {
		t.Fatalf("tenant usage_records count=%d want 1; override write must not insert/delete settled bills", tenantUsageCount)
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

func TestStoreListDoesNotCacheStaleSnapshotAfterInvalidate(t *testing.T) {
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
	frozen := time.Date(2026, 9, 11, 6, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return frozen.Add(3 * time.Second) }

	oldInput := decimal.RequireFromString("1")
	if _, err := store.Upsert(ctx, UpsertParams{
		Vendor: "openai", Model: "gpt-cache-gen",
		Rates:     RateBuckets{Input: &oldInput},
		Actor:     "admin_token:317",
		ActorRole: "platform_admin",
	}); err != nil {
		t.Fatalf("seed old override: %v", err)
	}

	newInput := decimal.RequireFromString("9")
	store.afterListLoad = func() {
		store.afterListLoad = nil
		store.Invalidate()
		if _, err := store.Upsert(ctx, UpsertParams{
			Vendor: "openai", Model: "gpt-cache-gen",
			Rates:     RateBuckets{Input: &newInput},
			Actor:     "admin_token:317",
			ActorRole: "platform_admin",
		}); err != nil {
			t.Fatalf("hook upsert: %v", err)
		}
	}

	staleView, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List during invalidate race: %v", err)
	}
	if len(staleView) != 1 || staleView[0].Rates.Input == nil || staleView[0].Rates.Input.String() != "1" {
		t.Fatalf("in-flight List should still observe the pre-write snapshot: %+v", staleView)
	}

	fresh, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List after invalidate race: %v", err)
	}
	if len(fresh) != 1 || fresh[0].Rates.Input == nil || fresh[0].Rates.Input.String() != "9" {
		t.Fatalf("cacheGen 守卫失效会把写前单价 1 写回缓存;第二次 List=%+v want input=9", fresh)
	}
}

func resetModelRateState(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `TRUNCATE model_rate_override_audit_log, model_rate_overrides RESTART IDENTITY`); err != nil {
		t.Fatalf("truncate model rate tables: %v", err)
	}
}

type settledUsageSeed struct {
	tenantID int64
	usageID  int64
}

type settledUsageSnapshot struct {
	rowJSON               string
	actualCost            string
	inputCost             string
	outputCost            string
	cacheReadCost         string
	cacheCreationCost     string
	tokensInput           int64
	tokensOutput          int64
	requestedModel        string
	pendingReconciliation bool
}

func seedSettledUsageRecord(t *testing.T, ctx context.Context, pool *pgxpool.Pool) settledUsageSeed {
	t.Helper()
	unique := "mr-settled-" + uuid.NewString()
	short := unique[:16]
	var (
		tenantID, userID, apiKeyID int64
		providerID, poolGroupID    int64
		channelID, accountID       int64
		claimID, usageID           int64
	)
	token := uuid.New()
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "tenant-"+unique).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`, tenantID, "user-"+short).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
		tenantID, userID, "key-"+short,
		"$2a$10$placeholder-not-resolved-by-modelrate-tests",
		"hk_test_"+short[:8],
	).Scan(&apiKeyID); err != nil {
		t.Fatalf("seed api_key: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, 10, 0)`, tenantID, userID); err != nil {
		t.Fatalf("seed user balance: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, 'openai_chat') RETURNING id`,
		tenantID, "provider-"+short, "Provider "+short,
	).Scan(&providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`, tenantID, "pool-"+short).Scan(&poolGroupID); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`, tenantID, poolGroupID, "channel-"+short).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, in_flight_count)
		 VALUES ($1, $2, $3, $4, 'api_key', 0) RETURNING id`,
		tenantID, providerID, channelID, "account-"+short,
	).Scan(&accountID); err != nil {
		t.Fatalf("seed provider account: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, pooling_group_id,
			billing_policy_version, request_class, provider_account_id, acquisition_token,
			attempt_seq, predicted_cost, currency_code, lease_expires_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, 'chat', 'gpt-settled-canary', $7,
			'1.0', 'standard', $8, $9,
			1, 1.23456789, 'USD', NOW() + interval '90 seconds'
		) RETURNING id`,
		tenantID, "idempotency-"+unique, "fingerprint-"+unique, apiKeyID, userID,
		"logical-"+unique, poolGroupID, accountID, token,
	).Scan(&claimID); err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO pool_slot_acquisitions (
			tenant_id, provider_account_id, acquisition_token, claim_id, attempt_seq, lease_expires_at
		) VALUES ($1, $2, $3, $4, 1, NOW() + interval '90 seconds')`,
		tenantID, accountID, token, claimID,
	); err != nil {
		t.Fatalf("seed pool slot: %v", err)
	}
	settledAt := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	if err := pool.QueryRow(ctx, `
		INSERT INTO usage_records (
			tenant_id, claim_id, api_key_id, user_id, provider_account_id,
			acquisition_token, attempt_seq,
			tokens_input, tokens_output,
			cache_creation_tokens, cache_read_tokens,
			cache_creation_5m_tokens, cache_creation_1h_tokens, image_output_tokens,
			actual_cost, input_cost, output_cost,
			cache_creation_cost, cache_read_cost, image_output_cost,
			end_class, usage_source, confidence_score, pending_reconciliation,
			stream_state, delivered_token_count, stream_terminated_reason,
			routing_reason, protocol_loss,
			requested_at, settled_at, requested_model, upstream_model,
			stream, snapshot_version, settlement_source, cost_snapshot
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, 1,
			321, 654,
			11, 22,
			3, 4, 0,
			'1.23456789'::numeric(20,8), '0.11111111'::numeric(20,8), '0.22222222'::numeric(20,8),
			'0.04444444'::numeric(20,8), '0.33333333'::numeric(20,8), 0,
			'non_streaming', 'reported', 1.0, false,
			2, 654, 'settled_canary',
			'{"route":"settled-canary"}'::jsonb, '[]'::jsonb,
			$7, $8, 'gpt-settled-canary', 'gpt-settled-canary',
			false, 'registry:canary;router:v0.1', 'provider_upstream', 'flat:canary-v1'
		) RETURNING id`,
		tenantID, claimID, apiKeyID, userID, accountID, token,
		settledAt.Add(-time.Second), settledAt,
	).Scan(&usageID); err != nil {
		t.Fatalf("seed settled usage_records: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM usage_record_dlq WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM usage_records WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM billing_events WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM pool_slot_acquisitions WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM balance_holds WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM user_balances WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM billing_ledger_claims WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM provider_accounts WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM channels WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM pool_groups WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM providers WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM api_keys WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})
	return settledUsageSeed{tenantID: tenantID, usageID: usageID}
}

func loadSettledUsageSnapshot(t *testing.T, ctx context.Context, pool *pgxpool.Pool, usageID int64) settledUsageSnapshot {
	t.Helper()
	var snap settledUsageSnapshot
	if err := pool.QueryRow(ctx, `
		SELECT row_to_json(t)::text,
		       actual_cost::numeric(20,8)::text,
		       input_cost::numeric(20,8)::text,
		       output_cost::numeric(20,8)::text,
		       cache_read_cost::numeric(20,8)::text,
		       cache_creation_cost::numeric(20,8)::text,
		       tokens_input, tokens_output, requested_model, pending_reconciliation
		  FROM usage_records t
		 WHERE id=$1`, usageID).Scan(
		&snap.rowJSON,
		&snap.actualCost,
		&snap.inputCost,
		&snap.outputCost,
		&snap.cacheReadCost,
		&snap.cacheCreationCost,
		&snap.tokensInput,
		&snap.tokensOutput,
		&snap.requestedModel,
		&snap.pendingReconciliation,
	); err != nil {
		t.Fatalf("load usage_records snapshot %d: %v", usageID, err)
	}
	return snap
}

func settledUsageSnapshotsEqual(before, after settledUsageSnapshot) bool {
	return before.rowJSON == after.rowJSON &&
		before.actualCost == after.actualCost &&
		before.inputCost == after.inputCost &&
		before.outputCost == after.outputCost &&
		before.cacheReadCost == after.cacheReadCost &&
		before.cacheCreationCost == after.cacheCreationCost &&
		before.tokensInput == after.tokensInput &&
		before.tokensOutput == after.tokensOutput &&
		before.requestedModel == after.requestedModel &&
		before.pendingReconciliation == after.pendingReconciliation
}
