//go:build integration_pg
// +build integration_pg

package settlementreconcile

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type reconcileSeed struct {
	tenantID          int64
	apiKeyID          int64
	userID            int64
	poolGroupID       int64
	providerID        int64
	channelID         int64
	providerAccountID int64
}

func TestReconcileOnceFinalizesAgedPendingRowsOnly(t *testing.T) {
	ctx := context.Background()
	pool := openReconcileIntegrationPool(t)
	t.Cleanup(pool.Close)

	seed := seedReconcileTenant(t, ctx, pool)
	var seededUsageIDs []int64
	t.Cleanup(func() {
		c := context.Background()
		for _, usageID := range seededUsageIDs {
			markUsageFinalizedForTest(t, c, pool, seed.tenantID, usageID)
		}
		// Migration 0039 makes money-path rows append-only, so this test leaves
		// the seeded graph in place. Unique tenant suffixes isolate runs, and
		// finalize markers keep these rows out of the global reconciler scan.
	})
	agedBase := time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	// The ONLY finalize-after-grace target: aged + inferred + $0 cost (no-authoritative-usage provisional).
	agedInferredZeroID := seedPendingUsageRecord(t, ctx, pool, seed, "aged-inferred-zero", "inferred", agedBase.Add(time.Second), "0")
	seededUsageIDs = append(seededUsageIDs, agedInferredZeroID)
	freshInferredZeroID := seedPendingUsageRecord(t, ctx, pool, seed, "fresh-inferred-zero", "inferred", time.Now(), "0")
	seededUsageIDs = append(seededUsageIDs, freshInferredZeroID)
	agedReportedID := seedPendingUsageRecord(t, ctx, pool, seed, "aged-reported", "reported", agedBase, "0")
	seededUsageIDs = append(seededUsageIDs, agedReportedID)
	// Partial-usage abnormal-EOF row: inferred but cost > 0. Must stay pending for genuine
	// reconciliation — never auto-finalized (else the worker would accept incomplete usage).
	agedInferredCostID := seedPendingUsageRecord(t, ctx, pool, seed, "aged-inferred-cost", "inferred", agedBase.Add(2*time.Second), "0.10")
	seededUsageIDs = append(seededUsageIDs, agedInferredCostID)

	reconciler := NewSettlementReconciler(pool, 50, 5*time.Minute)
	if _, err := reconciler.reconcileOnce(ctx); err != nil {
		t.Fatalf("reconcileOnce: %v", err)
	}

	// Per-row marker counts (robust to shared-DB pollution; we only check our seeded ids).
	// MUTATION: no finalize -> aged-inferred-zero marker missing -> RED.
	if got := finalizationEventCount(t, ctx, pool, agedInferredZeroID); got != 1 {
		t.Fatalf("aged inferred $0 usage_record %d markers=%d want 1", agedInferredZeroID, got)
	}
	// MUTATION: drop the grace filter -> fresh row finalized -> RED.
	if got := finalizationEventCount(t, ctx, pool, freshInferredZeroID); got != 0 {
		t.Fatalf("fresh inferred usage_record %d markers=%d want 0", freshInferredZeroID, got)
	}
	// MUTATION: drop usage_source='inferred' -> aged reported finalized -> RED.
	if got := finalizationEventCount(t, ctx, pool, agedReportedID); got != 0 {
		t.Fatalf("aged reported usage_record %d markers=%d want 0", agedReportedID, got)
	}
	// MUTATION: drop the actual_cost=0 filter -> aged inferred cost>0 (partial-EOF) finalized -> RED.
	if got := finalizationEventCount(t, ctx, pool, agedInferredCostID); got != 0 {
		t.Fatalf("aged inferred cost>0 (partial-EOF) usage_record %d markers=%d want 0", agedInferredCostID, got)
	}

	// P1-b: the finalize marker must record 0 authoritative tokens, NOT ur.tokens_output
	// (which is the delivered CONTENT-FRAME count 40 on the missing-usage path).
	// MUTATION: copy ur.tokens_output into authoritative_tokens_output -> 40 -> RED.
	aIn, aOut := finalizationMarkerTokens(t, ctx, pool, agedInferredZeroID)
	if aIn != 0 || aOut != 0 {
		t.Fatalf("finalize marker authoritative tokens=(in=%d,out=%d) want (0,0); must not label frame count as authoritative", aIn, aOut)
	}

	if !pendingReconciliation(t, ctx, pool, agedInferredZeroID) ||
		!pendingReconciliation(t, ctx, pool, freshInferredZeroID) ||
		!pendingReconciliation(t, ctx, pool, agedReportedID) ||
		!pendingReconciliation(t, ctx, pool, agedInferredCostID) {
		t.Fatal("usage_records pending_reconciliation flag must remain immutable; finalization is append-only")
	}
}

func openReconcileIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping settlementreconcile integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	return pool
}

func seedReconcileTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool) reconcileSeed {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	var seed reconcileSeed
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "reconcile-"+suffix).Scan(&seed.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`, seed.tenantID, "user-"+suffix).Scan(&seed.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
		seed.tenantID, seed.userID, "key-"+suffix,
		"$2a$10$placeholder-not-resolved-by-reconcile-tests", "hk_reconcile_"+suffix[:8],
	).Scan(&seed.apiKeyID); err != nil {
		t.Fatalf("seed api key: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`, seed.tenantID, "pool-"+suffix).Scan(&seed.poolGroupID); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, 'OpenAI', 'openai_chat') RETURNING id`,
		seed.tenantID, "openai-"+suffix,
	).Scan(&seed.providerID); err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`, seed.tenantID, seed.poolGroupID, "channel-"+suffix).Scan(&seed.channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, in_flight_count)
		 VALUES ($1, $2, $3, $4, 'api_key', 0) RETURNING id`,
		seed.tenantID, seed.providerID, seed.channelID, "acct-"+suffix,
	).Scan(&seed.providerAccountID); err != nil {
		t.Fatalf("seed provider account: %v", err)
	}
	return seed
}

func seedPendingUsageRecord(t *testing.T, ctx context.Context, pool *pgxpool.Pool, seed reconcileSeed, label, usageSource string, settledAt time.Time, actualCost string) int64 {
	t.Helper()
	acquisitionToken := uuid.New()
	var claimID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, pooling_group_id,
			billing_policy_version, request_class, provider_account_id, acquisition_token,
			attempt_seq, predicted_cost, actual_cost, currency_code, status, settled_at, lease_expires_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, 'chat', 'gpt-4o', $7,
			'test-policy', 'standard', $8, $9,
			1, 0.01, 0.02, 'USD', 'committed', $10, NOW() + interval '90 seconds'
		) RETURNING id`,
		seed.tenantID, "idem-"+label+"-"+uuid.NewString(), "fp-"+label+"-"+uuid.NewString(),
		seed.apiKeyID, seed.userID, "lr-"+label+"-"+uuid.NewString(), seed.poolGroupID,
		seed.providerAccountID, acquisitionToken, settledAt,
	).Scan(&claimID); err != nil {
		t.Fatalf("seed %s claim: %v", label, err)
	}

	var usageID int64
	// tokens_output=40 mimics the missing-usage path where tokens_output carries a delivered
	// CONTENT-FRAME count (not a token count); actualCost varies so tests can cover both the
	// $0 no-usage provisional and the cost>0 partial-EOF inferred row.
	if err := pool.QueryRow(ctx,
		`INSERT INTO usage_records (
			tenant_id, claim_id, api_key_id, user_id, provider_account_id,
			acquisition_token, attempt_seq, tokens_output, actual_cost, output_cost,
			end_class, usage_source, pending_reconciliation,
			requested_at, settled_at, requested_model, upstream_model, stream
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, 1, 40, $7, $7,
			'stream_end_graceful', $8, true,
			$9, $9, 'gpt-4o', 'gpt-4o', true
		) RETURNING id`,
		seed.tenantID, claimID, seed.apiKeyID, seed.userID, seed.providerAccountID,
		acquisitionToken, actualCost, usageSource, settledAt,
	).Scan(&usageID); err != nil {
		t.Fatalf("seed %s usage_record: %v", label, err)
	}
	return usageID
}

func pendingReconciliation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, usageID int64) bool {
	t.Helper()
	var pending bool
	if err := pool.QueryRow(ctx, `SELECT pending_reconciliation FROM usage_records WHERE id=$1`, usageID).Scan(&pending); err != nil {
		t.Fatalf("query pending_reconciliation for %d: %v", usageID, err)
	}
	return pending
}

func finalizationEventCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, usageID int64) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM usage_record_reconciliation_events
		 WHERE original_usage_record_id=$1 AND reconciliation_source='finalize_after_grace'`,
		usageID,
	).Scan(&count); err != nil {
		t.Fatalf("query finalization events for %d: %v", usageID, err)
	}
	return count
}

func finalizationMarkerTokens(t *testing.T, ctx context.Context, pool *pgxpool.Pool, usageID int64) (int64, int64) {
	t.Helper()
	var in, out int64
	if err := pool.QueryRow(ctx,
		`SELECT authoritative_tokens_input, authoritative_tokens_output
		 FROM usage_record_reconciliation_events
		 WHERE original_usage_record_id=$1 AND reconciliation_source='finalize_after_grace'`,
		usageID,
	).Scan(&in, &out); err != nil {
		t.Fatalf("query finalize marker tokens for %d: %v", usageID, err)
	}
	return in, out
}

func markUsageFinalizedForTest(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, usageID int64) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`INSERT INTO usage_record_reconciliation_events (
			tenant_id, original_usage_record_id,
			authoritative_tokens_input, authoritative_tokens_output,
			authoritative_cost, cost_delta, reconciliation_source
		)
		SELECT tenant_id, id, tokens_input, tokens_output, actual_cost, 0, 'finalize_after_grace'
		FROM usage_records ur
		WHERE tenant_id=$1 AND id=$2
		  AND NOT EXISTS (
			SELECT 1 FROM usage_record_reconciliation_events ure
			WHERE ure.tenant_id=ur.tenant_id
			  AND ure.original_usage_record_id=ur.id
			  AND ure.reconciliation_source='finalize_after_grace'
		  )`,
		tenantID, usageID,
	); err != nil {
		t.Fatalf("mark usage_record %d finalized for cleanup: %v", usageID, err)
	}
}
