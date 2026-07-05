//go:build integration_pg

package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/subscription"
)

// TestWorkerStatsAdapterPendingReconciliationUsesRealQuery 守 C-2 一级真实 SQL:
// worker-stats 只统计 pending_reconciliation=true 且尚无任何对账事件的 usage_records。
// 判别(§14):若 CountPendingReconciliationUsageRecords 去掉 WHERE/NOT EXISTS 条件,本测试播种的
// 非 pending 行或已手动补价行会被一并计入,usage_records 从 1 变 2/3 后转红。
func TestWorkerStatsAdapterPendingReconciliationUsesRealQuery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openWorkerStatsIntegrationPool(t, ctx)
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	queries := dbbilling.New(tx)
	baseline, err := queries.CountPendingReconciliationUsageRecords(ctx)
	if err != nil {
		t.Fatalf("count pending baseline: %v", err)
	}
	fixture := seedWorkerStatsUsageFixture(t, ctx, tx)
	seedWorkerStatsUsageRecord(t, ctx, tx, fixture, "pending", true)
	repricedID := seedWorkerStatsUsageRecord(t, ctx, tx, fixture, "repriced", true)
	seedWorkerStatsRepriceEvent(t, ctx, tx, fixture.tenantID, repricedID)
	seedWorkerStatsUsageRecord(t, ctx, tx, fixture, "settled", false)

	reminder := subscription.NewReminderWorker(subscription.ReminderWorkerConfig{})
	expiry := subscription.NewExpiryWorker(subscription.ExpiryWorkerConfig{})
	reader := newSubscriptionWorkerStatsReader(reminder, expiry, nil, queries)

	stats := reader.ReadWorkerStats(ctx)
	if stats.PendingReconciliation.UsageRecords != baseline+1 || stats.PendingReconciliation.QueryFailed {
		t.Fatalf("pending_reconciliation stats=%+v baseline=%d want usage_records=%d/query_failed=false", stats.PendingReconciliation, baseline, baseline+1)
	}
}

type workerStatsUsageFixture struct {
	tenantID int64
	userID   int64
	apiKeyID int64
}

func openWorkerStatsIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open pg pool: %v", err)
	}
	return pool
}

func seedWorkerStatsUsageFixture(t *testing.T, ctx context.Context, tx pgx.Tx) workerStatsUsageFixture {
	t.Helper()
	suffix := fmt.Sprintf("worker-stats-%d", time.Now().UnixNano())
	var fixture workerStatsUsageFixture
	if err := tx.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, suffix).Scan(&fixture.tenantID); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO users (tenant_id, email, display_name) VALUES ($1, $2, $3) RETURNING id`,
		fixture.tenantID, suffix+"@example.test", "Worker Stats").Scan(&fixture.userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		fixture.tenantID, fixture.userID, suffix, "hash-"+suffix, "ws-"+suffix[:8]).Scan(&fixture.apiKeyID); err != nil {
		t.Fatalf("insert api key: %v", err)
	}
	return fixture
}

func seedWorkerStatsUsageRecord(t *testing.T, ctx context.Context, tx pgx.Tx, fixture workerStatsUsageFixture, label string, pending bool) int64 {
	t.Helper()
	now := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	var claimID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, attempt_seq,
			billing_policy_version, request_class, predicted_cost, actual_cost,
			currency_code, status, settled_at, lease_expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'chat', 'worker-stats-model', 1,
			'bp-test', 'standard', 0, 0, 'USD', 'committed', $7, $8)
		RETURNING id
	`, fixture.tenantID, "idem-"+label, "fingerprint-"+label, fixture.apiKeyID, fixture.userID,
		"logical-"+label, now, now.Add(time.Hour)).Scan(&claimID); err != nil {
		t.Fatalf("insert claim %s: %v", label, err)
	}
	var usageID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO usage_records (
			tenant_id, claim_id, api_key_id, user_id, attempt_seq,
			tokens_input, tokens_output, actual_cost, input_cost, output_cost,
			end_class, usage_source, pending_reconciliation, requested_at, settled_at,
			requested_model, stream, settlement_source
		)
		VALUES ($1, $2, $3, $4, 1, 1, 1, 0, 0, 0, 'non_streaming',
			'reported', $5, $6, $7, 'worker-stats-model', false, 'response_cache_l2')
		RETURNING id
	`, fixture.tenantID, claimID, fixture.apiKeyID, fixture.userID, pending, now.Add(-time.Second), now).Scan(&usageID); err != nil {
		t.Fatalf("insert usage %s: %v", label, err)
	}
	return usageID
}

func seedWorkerStatsRepriceEvent(t *testing.T, ctx context.Context, tx pgx.Tx, tenantID, usageID int64) {
	t.Helper()
	if _, err := tx.Exec(ctx, `
		INSERT INTO usage_record_reconciliation_events (
			tenant_id,
			original_usage_record_id,
			authoritative_tokens_input,
			authoritative_tokens_output,
			authoritative_cost,
			cost_delta,
			reconciliation_source,
			reconciled_at
		) VALUES ($1, $2, 1, 1, 0, 0, 'manual_reprice_current_pricing', $3)`,
		tenantID,
		usageID,
		time.Date(2026, 7, 5, 12, 1, 0, 0, time.UTC),
	); err != nil {
		t.Fatalf("insert reprice event: %v", err)
	}
}
