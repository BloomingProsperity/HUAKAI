//go:build integration_pg

package billing

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestLatencyPercentiles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openUsageOutcomePool(t, ctx)
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin latency percentile tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	fixture := seedUsageOutcomeFixture(t, ctx, tx)
	model := fmt.Sprintf("perf-latency-%d", time.Now().UnixNano())
	base := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= 10; i++ {
		requestedAt := base.Add(time.Duration(i) * time.Second)
		seedUsagePerformanceRecord(t, ctx, tx, fixture, fmt.Sprintf("%s-%02d", model, i), model, requestedAt, base.Add(time.Hour), time.Duration(i*100)*time.Millisecond, 20, "stream_end_graceful")
	}

	q := New(tx)
	// MUTATION: changing percentile array to [0.5,0.5,0.5] makes P95/P99 collapse to P50 -> RED.
	got, err := q.AggregateUsageLatencyPercentiles(ctx, AggregateUsageLatencyPercentilesParams{
		SettledSince: pgtype.Timestamptz{Time: base.Add(-time.Minute), Valid: true},
		Model:        &model,
	})
	if err != nil {
		t.Fatalf("AggregateUsageLatencyPercentiles: %v", err)
	}
	if got.P50Ms < 540 || got.P50Ms > 560 {
		t.Fatalf("p50_ms=%f want about 550 from 100..1000ms", got.P50Ms)
	}
	if got.P95Ms < 940 || got.P95Ms > 970 {
		t.Fatalf("p95_ms=%f want about 955 from 100..1000ms", got.P95Ms)
	}
	if got.P99Ms < 980 || got.P99Ms > 1000 {
		t.Fatalf("p99_ms=%f want about 991 from 100..1000ms", got.P99Ms)
	}
	if !(got.P50Ms < got.P95Ms && got.P95Ms < got.P99Ms) {
		t.Fatalf("percentiles must be strictly increasing for seeded data: p50=%f p95=%f p99=%f", got.P50Ms, got.P95Ms, got.P99Ms)
	}
}

func TestPerfBucketed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openUsageOutcomePool(t, ctx)
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin perf bucket tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	fixture := seedUsageOutcomeFixture(t, ctx, tx)
	model := fmt.Sprintf("perf-bucket-%d", time.Now().UnixNano())
	base := time.Date(2026, 6, 8, 9, 15, 0, 0, time.UTC)
	seedUsagePerformanceRecord(t, ctx, tx, fixture, model+"-h1-a", model, base, base.Add(3*time.Hour), 100*time.Millisecond, 20, "stream_end_graceful")
	seedUsagePerformanceRecord(t, ctx, tx, fixture, model+"-h1-b", model, base.Add(10*time.Minute), base.Add(3*time.Hour), 200*time.Millisecond, 20, "stream_end_graceful")
	seedUsagePerformanceRecord(t, ctx, tx, fixture, model+"-h2-a", model, base.Add(time.Hour), base.Add(3*time.Hour), 300*time.Millisecond, 20, "upstream_error_5xx")

	q := New(tx)
	// MUTATION: removing date_trunc(bucket, requested_at) groups only by model and returns one row -> RED.
	rows, err := q.AggregateUsagePerformanceByModelBucketed(ctx, AggregateUsagePerformanceByModelBucketedParams{
		SettledSince: pgtype.Timestamptz{Time: base.Add(-time.Minute), Valid: true},
		Bucket:       "hour",
		RowLimit:     20,
	})
	if err != nil {
		t.Fatalf("AggregateUsagePerformanceByModelBucketed: %v", err)
	}
	var seen []AggregateUsagePerformanceByModelBucketedRow
	for _, row := range rows {
		if row.Key == model {
			seen = append(seen, row)
		}
	}
	if len(seen) != 2 {
		t.Fatalf("model bucket rows=%d want 2 hourly buckets; all rows=%v", len(seen), rows)
	}
	if !seen[0].Bucket.Time.Equal(time.Date(2026, 6, 8, 9, 0, 0, 0, time.UTC)) || !seen[1].Bucket.Time.Equal(time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("bucket times=%v/%v want 09:00Z and 10:00Z", seen[0].Bucket.Time, seen[1].Bucket.Time)
	}
}

func seedUsagePerformanceRecord(t *testing.T, ctx context.Context, tx pgx.Tx, f usageOutcomeFixture, logicalRequestID, model string, requestedAt, settledAt time.Time, ttft time.Duration, tokensOutput int32, endClass string) {
	t.Helper()
	acquisitionToken := uuid.New()
	var claimID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, pooling_group_id,
			provider_account_id, acquisition_token, attempt_seq, billing_policy_version,
			request_class, predicted_cost, actual_cost, currency_code, status, settled_at,
			lease_expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'messages', $7, $8,
			$9, $10, 1, 'bp-test', 'standard', 0.01000000, 0.01000000, 'USD',
			'committed', $11, $12)
		RETURNING id
	`, f.tenantID, "idem-"+logicalRequestID, "fingerprint-"+logicalRequestID, f.apiKeyID, f.userID, logicalRequestID, model, f.poolID, f.providerAccountID, acquisitionToken, settledAt, settledAt.Add(time.Hour)).Scan(&claimID); err != nil {
		t.Fatalf("insert claim %s: %v", logicalRequestID, err)
	}
	firstByteAt := requestedAt.Add(ttft)
	lastEventAt := firstByteAt.Add(time.Second)
	if _, err := tx.Exec(ctx, `
		INSERT INTO usage_records (
			tenant_id, claim_id, api_key_id, user_id, provider_account_id,
			acquisition_token, attempt_seq, tokens_input, tokens_output, actual_cost,
			input_cost, output_cost, end_class, usage_source, pending_reconciliation,
			requested_at, first_byte_at, last_event_at, settled_at, requested_model,
			upstream_model, stream, settlement_source
		)
		VALUES ($1, $2, $3, $4, $5, $6, 1, 10, $7, 0.01000000, 0.00400000,
			0.00600000, $8, 'reported', false, $9, $10, $11, $12, $13,
			$13, false, 'provider_upstream')
	`, f.tenantID, claimID, f.apiKeyID, f.userID, f.providerAccountID, acquisitionToken, tokensOutput, endClass, requestedAt, firstByteAt, lastEventAt, settledAt, model); err != nil {
		t.Fatalf("insert usage %s: %v", logicalRequestID, err)
	}
}
