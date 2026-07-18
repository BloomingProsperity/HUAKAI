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
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
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

func TestMismatchRefundCompletedPendingWithoutEvidenceRebuildsInPostgres(t *testing.T) {
	ctx := context.Background()
	pool := openRefundAtomicTestPool(t, ctx)
	seed := seedRefundAtomicClaim(t, ctx, pool, "completed-without-evidence")
	payload := seed.payload()
	if _, err := pool.Exec(ctx, `
INSERT INTO audit_refund_pending (
    claim_id, request_id, delta_micro_usd, status, created_at, completed_at, tenant_id
) VALUES ($1, $2, $3, 'completed', $4, $4, $5)`,
		payload.ClaimID, payload.RequestID, payload.DeltaMicroUSD, refundAtomicNow(), payload.TenantID,
	); err != nil {
		t.Fatalf("seed false completed pending: %v", err)
	}

	ledger, formatter := refundAtomicLedgerAndFormatter(t, ctx, pool, seed, 76)
	pending, err := NewPGXRefundPendingStore(pool)
	if err != nil {
		t.Fatalf("NewPGXRefundPendingStore: %v", err)
	}
	receiptStore, err := NewPGXReceiptStorage(pool)
	if err != nil {
		t.Fatalf("NewPGXReceiptStorage: %v", err)
	}
	worker := NewMismatchRefundWorker(pending, billing.NewSettler(pool), formatter,
		WithRefundLedger(ledger), WithRefundReceiptSink(receiptStore),
		WithRefundTxPool(pool), WithRefundNow(refundAtomicNow))

	if err := worker.Apply(ctx, payload); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	assertRefundAtomicRows(t, ctx, pool, seed, 1, 1, 1, "completed")
}

func TestMismatchRefundQuotaFailureRollsBackAndRetryCommitsOnce(t *testing.T) {
	ctx := context.Background()
	pool := openRefundAtomicTestPool(t, ctx)
	seed := seedRefundAtomicClaim(t, ctx, pool, "quota-rollback")
	ledger, formatter := refundAtomicLedgerAndFormatter(t, ctx, pool, seed, 74)
	pending, err := NewPGXRefundPendingStore(pool)
	if err != nil {
		t.Fatalf("NewPGXRefundPendingStore: %v", err)
	}
	receiptStore, err := NewPGXReceiptStorage(pool)
	if err != nil {
		t.Fatalf("NewPGXReceiptStorage: %v", err)
	}
	want := errors.New("quota reverse failed")
	quotaReverser := &refundAtomicQuotaReverser{err: want, requireOperationFact: true}
	worker := NewMismatchRefundWorker(pending, billing.NewSettler(pool), formatter,
		WithRefundLedger(ledger),
		WithRefundReceiptSink(receiptStore),
		WithRefundTxPool(pool),
		WithRefundQuotaReverser(quotaReverser),
		WithRefundNow(refundAtomicNow))

	err = worker.Apply(ctx, seed.payload())
	if !errors.Is(err, want) {
		t.Fatalf("Apply error=%v want %v", err, want)
	}
	if quotaReverser.txCalls != 1 || quotaReverser.legacyCalls != 0 || quotaReverser.lastAmount != seed.payload().DeltaMicroUSD {
		t.Fatalf("quota calls tx/legacy/amount=%d/%d/%d", quotaReverser.txCalls, quotaReverser.legacyCalls, quotaReverser.lastAmount)
	}
	assertRefundAtomicRows(t, ctx, pool, seed, 0, 0, 0, "failed")

	quotaReverser.err = nil
	quotaReverser.txCalls = 0
	if err := worker.Apply(ctx, seed.payload()); err != nil {
		t.Fatalf("retry Apply: %v", err)
	}
	if quotaReverser.txCalls != 1 || quotaReverser.legacyCalls != 0 {
		t.Fatalf("retry quota calls tx/legacy=%d/%d want 1/0", quotaReverser.txCalls, quotaReverser.legacyCalls)
	}
	assertRefundAtomicRows(t, ctx, pool, seed, 1, 1, 1, "completed")
}

func TestMismatchRefundIdempotencyConflictQuarantinesFirstReplay(t *testing.T) {
	ctx := context.Background()
	pool := openRefundAtomicTestPool(t, ctx)
	seed := seedRefundAtomicClaim(t, ctx, pool, "conflict-quarantine")
	settler := billing.NewSettler(pool)
	key := mismatchRefundIdempotencyKey(seed.claimID)
	if _, err := settler.Refund(ctx, billing.RefundRequest{
		TenantID: seed.tenantID, ClaimID: seed.claimID, AmountMicroUSD: 20,
		Reason: AuditMismatchRefundReason, IdempotencyKey: key,
		AuditRequestID: "preexisting-refund-trace", RequireExact: true,
	}); err != nil {
		t.Fatalf("seed conflicting refund operation: %v", err)
	}
	ledger, formatter := refundAtomicLedgerAndFormatter(t, ctx, pool, seed, 75)
	pending, err := NewPGXRefundPendingStore(pool)
	if err != nil {
		t.Fatalf("NewPGXRefundPendingStore: %v", err)
	}
	receiptStore, err := NewPGXReceiptStorage(pool)
	if err != nil {
		t.Fatalf("NewPGXReceiptStorage: %v", err)
	}
	worker := NewMismatchRefundWorker(pending, settler, formatter,
		WithRefundLedger(ledger), WithRefundReceiptSink(receiptStore),
		WithRefundTxPool(pool), WithRefundNow(refundAtomicNow))
	dlqService := dlq.NewService(dlq.NewStore(pool), dlq.WithClock(refundAtomicNow))
	dlqService.Register(dlq.EventKindAuditMismatchRefund, worker.Handler())
	event, err := NewMismatchRefundEvent(&CostReceipt{
		TenantID: seed.tenantID, ClaimID: seed.claimID, RequestID: seed.requestID,
	}, MismatchVerdict{
		State:             ReceiptValidationStateMismatchPending,
		DeltaMicroUSD:     seed.payload().DeltaMicroUSD,
		MismatchDirection: MismatchDirectionOverCharge,
	}, refundAtomicNow())
	if err != nil {
		t.Fatalf("NewMismatchRefundEvent: %v", err)
	}
	eventID, err := dlqService.Enqueue(ctx, event)
	if err != nil {
		t.Fatalf("enqueue refund event: %v", err)
	}
	if _, err := dlqService.Replay(ctx, eventID, "refund-conflict-test"); !errors.Is(err, dlq.ErrUnretryable) || !errors.Is(err, billing.ErrRefundIdempotencyConflict) {
		t.Fatalf("Replay error=%v want unretryable refund conflict", err)
	}
	var dlqStatus string
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT status, replay_attempts FROM usage_record_dlq WHERE tenant_id=$1 AND id=$2`,
		seed.tenantID, eventID).Scan(&dlqStatus, &attempts); err != nil {
		t.Fatalf("read quarantined refund event: %v", err)
	}
	if dlqStatus != string(dlq.StatusQuarantined) || attempts != 1 {
		t.Fatalf("dlq status=%q attempts=%d want quarantined/1", dlqStatus, attempts)
	}
	if got := countRefundAtomicRows(t, ctx, pool,
		`SELECT count(*) FROM billing_events WHERE tenant_id=$1 AND claim_id=$2 AND event_type='reconciliation_appended' AND actual_cost_signed < 0`,
		seed.tenantID, seed.claimID); got != 1 {
		t.Fatalf("refund billing events=%d want 1", got)
	}
	if got := countRefundAtomicRows(t, ctx, pool,
		`SELECT count(*) FROM billing_refund_operations WHERE tenant_id=$1 AND claim_id=$2`, seed.tenantID, seed.claimID); got != 1 {
		t.Fatalf("refund operations=%d want 1", got)
	}
	if got := countRefundAtomicRows(t, ctx, pool,
		`SELECT count(*) FROM audit_ledger_entries WHERE tenant_id=$1 AND request_id=$2`,
		seed.tenantID, refundAuditRequestID(seed.requestID, seed.claimID)); got != 0 {
		t.Fatalf("refund audit ledger entries=%d want 0", got)
	}
	if got := countRefundAtomicRows(t, ctx, pool,
		`SELECT count(*) FROM user_cost_receipts WHERE tenant_id=$1 AND request_id=$2 AND receipt_sequence=1`,
		seed.tenantID, seed.requestID); got != 0 {
		t.Fatalf("refunded receipts=%d want 0", got)
	}
	var pendingStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM audit_refund_pending WHERE claim_id=$1`, seed.claimID).Scan(&pendingStatus); err != nil {
		t.Fatalf("read refund pending status: %v", err)
	}
	if pendingStatus != "failed" {
		t.Fatalf("pending status=%q want failed", pendingStatus)
	}
	var balance decimal.Decimal
	if err := pool.QueryRow(ctx, `SELECT balance FROM user_balances WHERE tenant_id=$1 AND user_id=$2`, seed.tenantID, seed.userID).Scan(&balance); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	if !balance.Equal(decimal.RequireFromString("9.99978000")) {
		t.Fatalf("balance=%s want 9.99978000", balance)
	}
}

func TestMismatchRefundTransactionRetriesSerializationConflict(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openRefundAtomicTestPool(t, ctx)
	seed := seedRefundAtomicClaim(t, ctx, pool, "serialization-retry")
	sequenceRef := installRefundSerializationFailure(t, ctx, pool, seed.claimID)
	ledger, formatter := refundAtomicLedgerAndFormatter(t, ctx, pool, seed, 73)
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
		t.Fatalf("Apply should retry one serialization conflict: %v", err)
	}
	assertRefundAtomicRows(t, ctx, pool, seed, 1, 1, 1, "completed")
	var triggerCalls int64
	if err := pool.QueryRow(ctx, fmt.Sprintf(`SELECT last_value FROM %s`, sequenceRef)).Scan(&triggerCalls); err != nil {
		t.Fatalf("read serialization sequence: %v", err)
	}
	if triggerCalls != 2 {
		t.Fatalf("serialization trigger calls=%d want 2", triggerCalls)
	}
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

type refundAtomicQuotaReverser struct {
	err                  error
	txCalls              int
	legacyCalls          int
	lastAmount           int64
	requireOperationFact bool
}

func (r *refundAtomicQuotaReverser) ReverseSettledCost(_ context.Context, _, _ int64, amountMicroUSD int64) (QuotaReverseResult, error) {
	r.legacyCalls++
	r.lastAmount = amountMicroUSD
	return QuotaReverseResult{}, r.err
}

func (r *refundAtomicQuotaReverser) ReverseSettledCostInTx(ctx context.Context, tx pgx.Tx, tenantID, claimID int64, amountMicroUSD int64) (QuotaReverseResult, error) {
	r.txCalls++
	r.lastAmount = amountMicroUSD
	if r.requireOperationFact {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM billing_refund_operations WHERE tenant_id=$1 AND claim_id=$2`, tenantID, claimID).Scan(&count); err != nil {
			return QuotaReverseResult{}, err
		}
		if count != 1 {
			return QuotaReverseResult{}, fmt.Errorf("refund operation facts=%d want 1 before quota reversal", count)
		}
	}
	return QuotaReverseResult{}, r.err
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
	for _, table := range []string{"billing_events", "billing_refund_operations", "audit_ledger_entries", "user_cost_receipts", "audit_refund_pending"} {
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
	if _, err := pool.Exec(ctx,
		`INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, $3, 0)`,
		seed.tenantID, seed.userID, decimal.RequireFromString("9.99976000"),
	); err != nil {
		t.Fatalf("seed paid balance: %v", err)
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
	if _, err := pool.Exec(ctx,
		`INSERT INTO balance_holds (claim_id, tenant_id, user_id, amount, captured, state, resolved_at)
		 VALUES ($1, $2, $3, $4, $4, 'captured', $5)`,
		seed.claimID, seed.tenantID, seed.userID, decimal.RequireFromString("0.00024000"), refundAtomicNow(),
	); err != nil {
		t.Fatalf("seed captured balance hold: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM user_cost_receipts WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_refund_pending WHERE claim_id=$1`, seed.claimID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM usage_record_dlq WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_ledger_entries WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM billing_refund_operations WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM billing_events WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM balance_holds WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM billing_ledger_claims WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM user_balances WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM api_keys WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM tenants WHERE id=$1`, seed.tenantID)
	})
	return seed
}

func installRefundSerializationFailure(t *testing.T, ctx context.Context, pool *pgxpool.Pool, claimID int64) string {
	t.Helper()
	suffix := fmt.Sprintf("%d_%d", claimID, time.Now().UTC().UnixNano())
	sequenceName := "huakai_refund_retry_seq_" + suffix
	functionName := "huakai_refund_retry_fail_" + suffix
	triggerName := "huakai_refund_retry_trigger_" + suffix
	sequenceIdent := pgx.Identifier{"public", sequenceName}.Sanitize()
	functionIdent := pgx.Identifier{"public", functionName}.Sanitize()
	triggerIdent := pgx.Identifier{triggerName}.Sanitize()
	sequenceRef := "public." + sequenceName
	if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE SEQUENCE %s`, sequenceIdent)); err != nil {
		t.Fatalf("create refund retry sequence: %v", err)
	}
	createFunction := fmt.Sprintf(`
CREATE OR REPLACE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.claim_id = %d
		AND NEW.event_type = 'reconciliation_appended'
		AND nextval('%s'::regclass) = 1 THEN
		RAISE EXCEPTION 'forced refund serialization conflict' USING ERRCODE = '40001';
	END IF;
	RETURN NEW;
END;
$$`, functionIdent, claimID, sequenceRef)
	if _, err := pool.Exec(ctx, createFunction); err != nil {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP SEQUENCE IF EXISTS %s`, sequenceIdent))
		t.Fatalf("create refund retry function: %v", err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON billing_events FOR EACH ROW EXECUTE FUNCTION %s()`, triggerIdent, functionIdent)); err != nil {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, functionIdent))
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP SEQUENCE IF EXISTS %s`, sequenceIdent))
		t.Fatalf("create refund retry trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON billing_events`, triggerIdent))
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, functionIdent))
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP SEQUENCE IF EXISTS %s`, sequenceIdent))
	})
	return sequenceRef
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
		`SELECT count(*) FROM billing_refund_operations WHERE tenant_id=$1 AND claim_id=$2`,
		seed.tenantID, seed.claimID,
	); got != wantBilling {
		t.Fatalf("billing_refund_operations=%d want %d", got, wantBilling)
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
		    AND validation_state='mismatch_refunded'
		    AND jsonb_typeof(adjustment_refs)='array'
		    AND jsonb_array_length(adjustment_refs) > 0`,
		seed.tenantID, seed.requestID,
	); got != wantReceipt {
		t.Fatalf("refunded user_cost_receipts=%d want %d", got, wantReceipt)
	}
	var balance decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT balance FROM user_balances WHERE tenant_id=$1 AND user_id=$2`,
		seed.tenantID, seed.userID,
	).Scan(&balance); err != nil {
		t.Fatalf("refund balance: %v", err)
	}
	wantBalance := decimal.RequireFromString("9.99976000")
	if wantBilling > 0 {
		wantBalance = decimal.RequireFromString("9.99980000")
	}
	if !balance.Equal(wantBalance) {
		t.Fatalf("refund balance=%s want %s", balance, wantBalance)
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
