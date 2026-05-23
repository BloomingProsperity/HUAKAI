package audit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestAT_AUDIT_001_047_RefundTxAllOrNothing(t *testing.T) {
	ctx := context.Background()
	pool := openRefundAtomicTestPool(t, ctx)
	seed := seedRefundAtomicClaim(t, ctx, pool, "rollback")
	ledger, formatter := refundAtomicLedgerAndFormatter(t, ctx, pool, seed, 71)
	pending, err := NewPGXRefundPendingStore(pool)
	if err != nil {
		t.Fatalf("NewPGXRefundPendingStore: %v", err)
	}
	want := errors.New("receipt append failed after ledger")
	worker := NewMismatchRefundWorker(pending, billing.NewSettler(pool), formatter,
		WithRefundLedger(ledger),
		WithRefundReceiptSink(&failingRefundReceiptTxSink{err: want}),
		WithRefundTxPool(pool),
		WithRefundNow(refundAtomicNow))

	err = worker.Apply(ctx, seed.payload())
	if !errors.Is(err, want) {
		t.Fatalf("Apply error=%v want %v", err, want)
	}
	assertRefundAtomicRows(t, ctx, pool, seed, 0, 0, 0, "failed")

	receiptStore, err := NewPGXReceiptStorage(pool)
	if err != nil {
		t.Fatalf("NewPGXReceiptStorage: %v", err)
	}
	retry := NewMismatchRefundWorker(pending, billing.NewSettler(pool), formatter,
		WithRefundLedger(ledger),
		WithRefundReceiptSink(receiptStore),
		WithRefundTxPool(pool),
		WithRefundNow(refundAtomicNow))
	if err := retry.Apply(ctx, seed.payload()); err != nil {
		t.Fatalf("retry Apply: %v", err)
	}
	assertRefundAtomicRows(t, ctx, pool, seed, 1, 1, 1, "completed")
}

func TestAT_AUDIT_001_048_RefundTxCommitOnAllSuccess(t *testing.T) {
	ctx := context.Background()
	pool := openRefundAtomicTestPool(t, ctx)
	seed := seedRefundAtomicClaim(t, ctx, pool, "commit")
	ledger, formatter := refundAtomicLedgerAndFormatter(t, ctx, pool, seed, 72)
	pending, err := NewPGXRefundPendingStore(pool)
	if err != nil {
		t.Fatalf("NewPGXRefundPendingStore: %v", err)
	}
	receiptStore, err := NewPGXReceiptStorage(pool)
	if err != nil {
		t.Fatalf("NewPGXReceiptStorage: %v", err)
	}
	worker := NewMismatchRefundWorker(pending, billing.NewSettler(pool), formatter,
		WithRefundLedger(ledger),
		WithRefundReceiptSink(receiptStore),
		WithRefundTxPool(pool),
		WithRefundNow(refundAtomicNow))

	if err := worker.Apply(ctx, seed.payload()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	assertRefundAtomicRows(t, ctx, pool, seed, 1, 1, 1, "completed")
}

type refundAtomicSeed struct {
	tenantID  int64
	apiKeyID  int64
	userID    int64
	claimID   int64
	requestID string
}

func (s refundAtomicSeed) payload() MismatchRefundPayload {
	return MismatchRefundPayload{
		TenantID:       s.tenantID,
		ClaimID:        s.claimID,
		RequestID:      s.requestID,
		DeltaMicroUSD:  40,
		FieldsMismatch: []string{"cost_total_micro_usd"},
		CreatedAt:      refundAtomicNow().Format(time.RFC3339Nano),
	}
}

func refundAtomicNow() time.Time {
	return time.Date(2026, 5, 18, 13, 0, 0, 0, time.UTC)
}

type failingRefundReceiptTxSink struct {
	err error
}

func (s *failingRefundReceiptTxSink) AppendRefundReceipt(context.Context, *CostReceipt) error {
	return s.err
}

func (s *failingRefundReceiptTxSink) AppendRefundReceiptInTx(context.Context, pgx.Tx, *CostReceipt) error {
	return s.err
}

type refundAtomicReceiptSource struct {
	inputs ReceiptInputs
}

func (s *refundAtomicReceiptSource) LookupReceiptInputs(_ context.Context, _ string, _ int64) (ReceiptInputs, error) {
	return s.inputs, nil
}

func openRefundAtomicTestPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping refund transaction integration test")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	for _, table := range []string{"billing_events", "audit_ledger_entries", "user_cost_receipts", "audit_refund_pending"} {
		var exists int
		if err := pool.QueryRow(ctx,
			"SELECT 1 FROM information_schema.tables WHERE table_name=$1 LIMIT 1",
			table,
		).Scan(&exists); err != nil {
			t.Skipf("%s table missing; migrations not applied: %v", table, err)
		}
	}
	return pool
}

func seedRefundAtomicClaim(t *testing.T, ctx context.Context, pool *pgxpool.Pool, label string) refundAtomicSeed {
	t.Helper()
	suffix := fmt.Sprintf("refund-tx-%s-%d", label, time.Now().UnixNano())
	seed := refundAtomicSeed{requestID: "req-" + suffix}
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"tenant-"+suffix,
	).Scan(&seed.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		seed.tenantID, "user-"+suffix,
	).Scan(&seed.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
		seed.tenantID,
		seed.userID,
		"key-"+suffix,
		"$2a$10$placeholder-not-resolved-by-refund-tx-tests",
		"hk_test_"+suffix[:8],
	).Scan(&seed.apiKeyID); err != nil {
		t.Fatalf("seed api key: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model,
			billing_policy_version, request_class, predicted_cost, actual_cost,
			currency_code, status, settled_at, lease_expires_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, 'chat', 'gpt-4o',
			'1.0', 'standard', $7, $8,
			'USD', 'committed', $9, $10
		) RETURNING id`,
		seed.tenantID,
		"idempotency-"+suffix,
		"fingerprint-"+suffix,
		seed.apiKeyID,
		seed.userID,
		"logical-"+suffix,
		decimal.RequireFromString("0.00024000"),
		decimal.RequireFromString("0.00024000"),
		refundAtomicNow(),
		refundAtomicNow().Add(time.Minute),
	).Scan(&seed.claimID); err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	return seed
}

func refundAtomicLedgerAndFormatter(t *testing.T, ctx context.Context, pool *pgxpool.Pool, seed refundAtomicSeed, signerSeed byte) (*auditledger.PostgresLedger, *ReceiptFormatter) {
	t.Helper()
	signer := testAuditSigner(t, signerSeed)
	ledger, err := auditledger.NewPostgresLedger(pool, signer)
	if err != nil {
		t.Fatalf("NewPostgresLedger: %v", err)
	}
	if _, err := ledger.Append(ctx, mustPrepareAuditLedgerEntry(t, ctx, auditledger.LedgerEntry{
		RequestID: seed.requestID,
		TenantID:  seed.tenantID,
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4o",
			RouteDecided:     "gpt-4o",
			UpstreamReported: "gpt-4o",
			Verdict:          "mismatch",
		},
	})); err != nil {
		t.Fatalf("append source ledger: %v", err)
	}
	formatter, err := NewReceiptFormatter(ledger, nil, &refundAtomicReceiptSource{inputs: ReceiptInputs{
		TenantID:            seed.tenantID,
		UserID:              seed.userID,
		ClaimID:             seed.claimID,
		Model:               "gpt-4o",
		InputTokens:         100,
		OutputTokens:        20,
		CachedTokens:        0,
		CostUSDMicros:       240,
		RateTableSnapshotID: 12,
		CreatedAt:           refundAtomicNow(),
	}}, signer, WithReceiptNow(refundAtomicNow))
	if err != nil {
		t.Fatalf("NewReceiptFormatter: %v", err)
	}
	return ledger, formatter
}

func assertRefundAtomicRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, seed refundAtomicSeed, wantBilling, wantLedger, wantReceipt int64, wantStatus string) {
	t.Helper()
	refundRequestID := refundAuditRequestID(seed.requestID, seed.claimID)
	if got := countRefundAtomicRows(t, ctx, pool,
		`SELECT count(*) FROM billing_events
		  WHERE tenant_id=$1 AND claim_id=$2 AND event_type='reconciliation_appended' AND audit_request_id=$3`,
		seed.tenantID, seed.claimID, refundRequestID,
	); got != wantBilling {
		t.Fatalf("refund billing_events=%d want %d", got, wantBilling)
	}
	if got := countRefundAtomicRows(t, ctx, pool,
		`SELECT count(*) FROM audit_ledger_entries WHERE tenant_id=$1 AND request_id=$2`,
		seed.tenantID, refundRequestID,
	); got != wantLedger {
		t.Fatalf("refund audit_ledger_entries=%d want %d", got, wantLedger)
	}
	if got := countRefundAtomicRows(t, ctx, pool,
		`SELECT count(*) FROM user_cost_receipts
		  WHERE tenant_id=$1 AND request_id=$2 AND receipt_sequence=1
		    AND validation_state='mismatch_refunded'`,
		seed.tenantID, seed.requestID,
	); got != wantReceipt {
		t.Fatalf("refunded user_cost_receipts=%d want %d", got, wantReceipt)
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM audit_refund_pending WHERE claim_id=$1`,
		seed.claimID,
	).Scan(&status); err != nil {
		t.Fatalf("refund pending status: %v", err)
	}
	if status != wantStatus {
		t.Fatalf("refund pending status=%q want %q", status, wantStatus)
	}
}

func countRefundAtomicRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, args ...any) int64 {
	t.Helper()
	var got int64
	if err := pool.QueryRow(ctx, query, args...).Scan(&got); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return got
}
