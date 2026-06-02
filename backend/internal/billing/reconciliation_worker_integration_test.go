//go:build integration_pg

package billing

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

func TestPendingReconciliationFinalizerMarksOnlyNoUsageStreamRows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pg := openPool(t, ctx)
	seed := seedSettlerGraph(t, ctx, pg, "pending-no-usage-finalizer")
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	eligibleID := insertPendingUsageRecordForReconciliationTest(t, ctx, pg, seed, pendingUsageRecordFixture{
		source:       "inferred",
		tokensOutput: 0,
		delivered:    40,
		settledAt:    now.Add(-10 * time.Minute),
	})
	reportedID := insertPendingUsageRecordForReconciliationTest(t, ctx, pg, seed, pendingUsageRecordFixture{
		source:       "reported",
		tokensOutput: 0,
		delivered:    41,
		settledAt:    now.Add(-10 * time.Minute),
	})
	outputID := insertPendingUsageRecordForReconciliationTest(t, ctx, pg, seed, pendingUsageRecordFixture{
		source:       "inferred",
		tokensOutput: 7,
		delivered:    7,
		settledAt:    now.Add(-10 * time.Minute),
	})
	alreadyID := insertPendingUsageRecordForReconciliationTest(t, ctx, pg, seed, pendingUsageRecordFixture{
		source:       "inferred",
		tokensOutput: 0,
		delivered:    42,
		settledAt:    now.Add(-10 * time.Minute),
	})
	insertNoUsageFinalizationMarker(t, ctx, pg, seed.tenantID, alreadyID, now.Add(-9*time.Minute))

	finalizer := NewPostgresPendingReconciliationFinalizer(pg)
	got, err := finalizer.FinalizePendingNoUsage(
		ctx,
		now.Add(-5*time.Minute),
		100,
		PendingReconciliationSourceStreamNoUsageFinalized,
		now,
	)
	if err != nil {
		t.Fatalf("FinalizePendingNoUsage: %v", err)
	}
	if got != 1 {
		t.Fatalf("finalized=%d want 1", got)
	}

	assertNoUsageMarkerCount(t, ctx, pg, seed.tenantID, eligibleID, 1)
	assertNoUsageMarkerCount(t, ctx, pg, seed.tenantID, reportedID, 0)
	assertNoUsageMarkerCount(t, ctx, pg, seed.tenantID, outputID, 0)
	assertNoUsageMarkerCount(t, ctx, pg, seed.tenantID, alreadyID, 1)

	q := dbbilling.New(pg)
	all, err := q.CountUsageRecords(ctx, dbbilling.CountUsageRecordsParams{
		TenantID:                  &seed.tenantID,
		PendingReconciliationOnly: false,
	})
	if err != nil {
		t.Fatalf("CountUsageRecords all: %v", err)
	}
	if all != 4 {
		t.Fatalf("all usage records=%d want 4", all)
	}
	pending, err := q.CountUsageRecords(ctx, dbbilling.CountUsageRecordsParams{
		TenantID:                  &seed.tenantID,
		PendingReconciliationOnly: true,
	})
	if err != nil {
		t.Fatalf("CountUsageRecords pending: %v", err)
	}
	if pending != 2 {
		t.Fatalf("pending usage records=%d want 2", pending)
	}
	rows, err := q.ListUsageRecords(ctx, dbbilling.ListUsageRecordsParams{
		TenantID:                  &seed.tenantID,
		PendingReconciliationOnly: true,
		PageLimit:                 10,
	})
	if err != nil {
		t.Fatalf("ListUsageRecords pending: %v", err)
	}
	gotIDs := map[int64]bool{}
	for _, row := range rows {
		gotIDs[row.ID] = true
	}
	for _, id := range []int64{reportedID, outputID} {
		if !gotIDs[id] {
			t.Fatalf("pending list missing unreconciled row %d; got %v", id, gotIDs)
		}
	}
	for _, id := range []int64{eligibleID, alreadyID} {
		if gotIDs[id] {
			t.Fatalf("pending list included finalized row %d; got %v", id, gotIDs)
		}
	}
}

type pendingUsageRecordFixture struct {
	source       string
	tokensOutput int32
	delivered    int64
	settledAt    time.Time
}

func insertPendingUsageRecordForReconciliationTest(t *testing.T, ctx context.Context, pg *pgxpool.Pool, seed settlerSeed, row pendingUsageRecordFixture) int64 {
	t.Helper()
	var id int64
	if err := pg.QueryRow(ctx, `
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
			stream, snapshot_version, settlement_source
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, 1,
			0, $7,
			0, 0,
			0, 0, 0,
			$8, 0, 0,
			0, 0, 0,
			'stream_end_no_terminal_marker', $9, 1.0, true,
			$10, $11, 'upstream_eof_no_terminal',
			'{}'::jsonb, '[]'::jsonb,
			$12, $13, 'gpt-4.1-mini', 'gpt-4.1-mini',
			true, 'registry:99:7;router:v0.1-phase-c', 'provider_upstream'
		)
		RETURNING id`,
		seed.tenantID,
		seed.claimID,
		seed.apiKeyID,
		seed.userID,
		seed.providerAccountID,
		seed.acquisitionToken,
		row.tokensOutput,
		decimal.Zero,
		row.source,
		StreamStatePartial.DBValue(),
		row.delivered,
		row.settledAt,
		row.settledAt,
	).Scan(&id); err != nil {
		t.Fatalf("insert usage record fixture: %v", err)
	}
	return id
}

func insertNoUsageFinalizationMarker(t *testing.T, ctx context.Context, pg *pgxpool.Pool, tenantID, usageRecordID int64, reconciledAt time.Time) {
	t.Helper()
	if _, err := pg.Exec(ctx, `
		INSERT INTO usage_record_reconciliation_events (
			tenant_id,
			original_usage_record_id,
			authoritative_tokens_input,
			authoritative_tokens_output,
			authoritative_cost,
			cost_delta,
			reconciliation_source,
			reconciled_at
		) VALUES ($1, $2, 0, 0, 0, 0, $3, $4)`,
		tenantID,
		usageRecordID,
		PendingReconciliationSourceStreamNoUsageFinalized,
		reconciledAt,
	); err != nil {
		t.Fatalf("insert reconciliation marker: %v", err)
	}
}

func assertNoUsageMarkerCount(t *testing.T, ctx context.Context, pg *pgxpool.Pool, tenantID, usageRecordID int64, want int) {
	t.Helper()
	var got int
	if err := pg.QueryRow(ctx, `
		SELECT count(*)::integer
		FROM usage_record_reconciliation_events
		WHERE tenant_id = $1
		  AND original_usage_record_id = $2
		  AND reconciliation_source = $3`,
		tenantID,
		usageRecordID,
		PendingReconciliationSourceStreamNoUsageFinalized,
	).Scan(&got); err != nil {
		t.Fatalf("count reconciliation markers: %v", err)
	}
	if got != want {
		t.Fatalf("usage_record %d marker count=%d want %d", usageRecordID, got, want)
	}
}
