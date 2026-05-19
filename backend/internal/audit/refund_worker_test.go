package audit

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestAT_AUDIT_001_019_MismatchDetectTriggersEnqueue(t *testing.T) {
	ctx := context.Background()
	derived := refundTestReceipt("req-refund-enqueue", 1001, 200)
	submitted := refundTestReceipt("req-refund-enqueue", 1001, 240)

	verdict, err := DetectReceiptMismatch(derived, submitted)
	if err != nil {
		t.Fatalf("DetectReceiptMismatch: %v", err)
	}
	if verdict.State != ReceiptValidationStateMismatchPending ||
		verdict.DeltaMicroUSD != 40 ||
		verdict.MismatchDirection != MismatchDirectionOverCharge ||
		!verdict.RefundEligible() {
		t.Fatalf("verdict=%+v", verdict)
	}
	if len(verdict.FieldsMismatch) != 1 || verdict.FieldsMismatch[0] != "cost_total_micro_usd" {
		t.Fatalf("fields_mismatch=%+v", verdict.FieldsMismatch)
	}

	queue := NewMismatchRefundQueue(&recordingMismatchRefundQueue{}, WithRefundNow(fixedRefundNow))
	eventID, err := queue.EnqueueMismatchRefund(ctx, derived, verdict)
	if err != nil {
		t.Fatalf("EnqueueMismatchRefund: %v", err)
	}
	if eventID != 77 {
		t.Fatalf("event id=%d want 77", eventID)
	}
	recorder := queue.service.(*recordingMismatchRefundQueue)
	if recorder.event.EventKind != dlq.EventKindAuditMismatchRefund || recorder.event.Lane != dlq.LaneHigh {
		t.Fatalf("event lane/kind=%s/%s", recorder.event.EventKind, recorder.event.Lane)
	}
	if recorder.event.IdempotencyKey != "audit_mismatch_refund:1001" {
		t.Fatalf("idempotency=%q", recorder.event.IdempotencyKey)
	}
}

func TestAT_AUDIT_001_020_RefundWorkerCallsBillingRefund(t *testing.T) {
	ctx := context.Background()
	worker, settler, _, _, payload := refundWorkerFixture(t, nil)

	if err := worker.Apply(ctx, payload); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if settler.refundCalls != 1 {
		t.Fatalf("refund calls=%d want 1", settler.refundCalls)
	}
	if settler.lastRefund.ClaimID != payload.ClaimID || settler.lastRefund.AmountMicroUSD != payload.DeltaMicroUSD ||
		settler.lastRefund.Reason != AuditMismatchRefundReason {
		t.Fatalf("refund request=%+v payload=%+v", settler.lastRefund, payload)
	}
}

func TestAT_AUDIT_001_021_DuplicateClaimRefundsOnce(t *testing.T) {
	ctx := context.Background()
	worker, settler, store, _, payload := refundWorkerFixture(t, nil)

	if err := worker.Apply(ctx, payload); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := worker.Apply(ctx, payload); err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if settler.refundCalls != 1 {
		t.Fatalf("refund calls=%d want 1", settler.refundCalls)
	}
	if got := store.Status(payload.ClaimID); got != "completed" {
		t.Fatalf("pending status=%q want completed", got)
	}
}

func TestAT_AUDIT_001_022_RefundedReceiptIsSignedWithRefundedState(t *testing.T) {
	ctx := context.Background()
	worker, _, _, sink, payload := refundWorkerFixture(t, nil)

	if err := worker.Apply(ctx, payload); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if sink.receipt == nil {
		t.Fatal("refunded receipt not recorded")
	}
	if sink.receipt.ValidationState != ReceiptValidationStateMismatchRefunded ||
		sink.receipt.ReceiptSequence != 1 ||
		sink.receipt.Verdict != ReceiptVerdictMismatchRefundPending ||
		len(sink.receipt.AdjustmentRefs) == 0 ||
		len(sink.receipt.SignedHash) == 0 {
		t.Fatalf("refunded receipt=%+v", sink.receipt)
	}
}

func TestAT_AUDIT_001_023_RefundFailureReturnsErrorForDLQRetry(t *testing.T) {
	ctx := context.Background()
	want := errors.New("refund backend down")
	worker, _, store, _, payload := refundWorkerFixture(t, want)

	err := worker.Apply(ctx, payload)
	if !errors.Is(err, want) {
		t.Fatalf("Apply error=%v want %v", err, want)
	}
	if got := store.Status(payload.ClaimID); got != "failed" {
		t.Fatalf("pending status=%q want failed", got)
	}
}

func TestAT_AUDIT_001_033_RefundLedgerAppendDuplicateRetryCompletes(t *testing.T) {
	ctx := context.Background()
	worker, _, store, sink, payload := refundWorkerFixture(t, nil)
	worker.ledger = &duplicateRefundLedger{
		entry: auditledger.LedgerEntry{
			LedgerID:  "ldg_refund_existing",
			RequestID: refundAuditRequestID(payload.RequestID, payload.ClaimID),
			TenantID:  payload.TenantID,
		},
	}

	if err := worker.Apply(ctx, payload); err != nil {
		t.Fatalf("Apply with duplicate refund ledger: %v", err)
	}
	if got := store.Status(payload.ClaimID); got != "completed" {
		t.Fatalf("pending status=%q want completed", got)
	}
	ledger := worker.ledger.(*duplicateRefundLedger)
	if ledger.appendCalls != 1 || ledger.lookupCalls != 1 {
		t.Fatalf("ledger calls append=%d lookup=%d", ledger.appendCalls, ledger.lookupCalls)
	}
	if sink.receipt == nil || !receiptHasAdjustmentRef(sink.receipt, "audit_ledger:ldg_refund_existing") {
		t.Fatalf("refunded receipt missing existing ledger ref: %+v", sink.receipt)
	}
}

func TestAT_AUDIT_001_034_RefundReceiptAppendDuplicateRetryCompletes(t *testing.T) {
	ctx := context.Background()
	worker, _, store, _, payload := refundWorkerFixture(t, nil)
	existing := refundTestReceipt(payload.RequestID, payload.ClaimID, 240)
	existing.TenantID = payload.TenantID
	existing.ReceiptSequence = 1
	existing.ValidationState = ReceiptValidationStateMismatchRefunded
	existing.Verdict = ReceiptVerdictMismatchRefundPending
	existing.AdjustmentRefs = []string{refundReceiptIdempotencyKey(payload)}
	existing.SignerFingerprint = []byte("existing-fingerprint")
	existing.SignedHash = []byte("existing-signature")
	sink := &duplicateRefundReceiptSink{existing: existing}
	worker.receiptSink = sink

	if err := worker.Apply(ctx, payload); err != nil {
		t.Fatalf("Apply with duplicate refund receipt: %v", err)
	}
	if got := store.Status(payload.ClaimID); got != "completed" {
		t.Fatalf("pending status=%q want completed", got)
	}
	if sink.appendCalls != 0 || sink.lookupCalls != 1 {
		t.Fatalf("receipt sink calls append=%d lookup=%d", sink.appendCalls, sink.lookupCalls)
	}
}

func TestAT_AUDIT_001_065_RefundRetryUsesExistingReceiptIdempotently(t *testing.T) {
	ctx := context.Background()
	worker, settler, store, sink, payload := refundWorkerFixture(t, nil)

	if err := worker.Apply(ctx, payload); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	store.mu.Lock()
	rec := store.rows[payload.ClaimID]
	rec.Status = "failed"
	store.rows[payload.ClaimID] = rec
	store.mu.Unlock()

	if err := worker.Apply(ctx, payload); err != nil {
		t.Fatalf("retry Apply: %v", err)
	}
	if settler.refundCalls != 1 {
		t.Fatalf("refund calls=%d want 1", settler.refundCalls)
	}
	if len(sink.receipts) != 1 {
		t.Fatalf("receipt count=%d want 1", len(sink.receipts))
	}
	if got := store.Status(payload.ClaimID); got != "completed" {
		t.Fatalf("pending status=%q want completed", got)
	}
	if sink.idempotencyLookups < 2 {
		t.Fatalf("idempotency lookups=%d want at least 2", sink.idempotencyLookups)
	}
	if !receiptHasAdjustmentRef(sink.receipt, refundReceiptIdempotencyKey(payload)) {
		t.Fatalf("refunded receipt missing idempotency ref: %+v", sink.receipt)
	}
}

func TestAT_AUDIT_001_refund_dup_retry_idempotent(t *testing.T) {
	ctx := context.Background()
	worker, settler, store, _, payload := refundWorkerFixture(t, nil)
	existing := refundTestReceipt(payload.RequestID, payload.ClaimID, 240)
	existing.TenantID = payload.TenantID
	existing.ReceiptSequence = 7
	existing.ValidationState = ReceiptValidationStateMismatchRefunded
	existing.Verdict = ReceiptVerdictMismatchRefundPending
	existing.AdjustmentRefs = []string{refundReceiptIdempotencyKey(payload)}
	existing.SignerFingerprint = []byte("existing-fingerprint")
	existing.SignedHash = []byte("existing-signature")
	sink := &duplicateRefundReceiptSink{existing: existing}
	worker.receiptSink = sink

	if err := worker.Apply(ctx, payload); err != nil {
		t.Fatalf("Apply with already-written refunded receipt: %v", err)
	}
	if got := store.Status(payload.ClaimID); got != "completed" {
		t.Fatalf("pending status=%q want completed", got)
	}
	if settler.refundCalls != 0 {
		t.Fatalf("refund calls=%d want 0", settler.refundCalls)
	}
	if sink.appendCalls != 0 || sink.lookupCalls != 1 {
		t.Fatalf("receipt sink calls append=%d lookup=%d", sink.appendCalls, sink.lookupCalls)
	}
}

func TestAT_AUDIT_001_061_MultipleRefundReceiptSequencesIncrement(t *testing.T) {
	ctx := context.Background()
	worker, _, _, sink, payload := refundWorkerFixture(t, nil)

	if err := worker.Apply(ctx, payload); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	second := payload
	second.ClaimID = 1002
	second.DeltaMicroUSD = 20
	if err := worker.Apply(ctx, second); err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if len(sink.receipts) != 2 {
		t.Fatalf("receipt count=%d want 2", len(sink.receipts))
	}
	if sink.receipts[0].ReceiptSequence != 1 || sink.receipts[1].ReceiptSequence != 2 {
		t.Fatalf("receipt sequences=%d,%d want 1,2", sink.receipts[0].ReceiptSequence, sink.receipts[1].ReceiptSequence)
	}
}

func refundWorkerFixture(t *testing.T, refundErr error) (*MismatchRefundWorker, *recordingRefundSettler, *MemoryRefundPendingStore, *recordingRefundReceiptSink, MismatchRefundPayload) {
	t.Helper()
	ctx := context.Background()
	requestID := "req-refund-worker"
	signer := testAuditSigner(t, 51)
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if _, err := ledger.Append(ctx, auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  9,
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4o",
			RouteDecided:     "gpt-4o",
			UpstreamReported: "gpt-4o",
			Verdict:          "mismatch",
		},
	}); err != nil {
		t.Fatalf("append ledger: %v", err)
	}
	formatter, err := NewReceiptFormatter(ledger, nil, &staticReceiptSource{inputs: ReceiptInputs{
		TenantID:            9,
		ClaimID:             1001,
		Model:               "gpt-4o",
		InputTokens:         100,
		OutputTokens:        20,
		CostUSDMicros:       240,
		RateTableSnapshotID: 12,
		CreatedAt:           fixedRefundNow(),
	}}, signer)
	if err != nil {
		t.Fatalf("formatter: %v", err)
	}
	store := NewMemoryRefundPendingStore()
	settler := &recordingRefundSettler{refundErr: refundErr}
	sink := &recordingRefundReceiptSink{}
	worker := NewMismatchRefundWorker(store, settler, formatter,
		WithRefundLedger(ledger),
		WithRefundReceiptSink(sink),
		WithRefundNow(fixedRefundNow))
	return worker, settler, store, sink, MismatchRefundPayload{
		TenantID:       9,
		ClaimID:        1001,
		RequestID:      requestID,
		DeltaMicroUSD:  40,
		FieldsMismatch: []string{"cost_total_micro_usd"},
		CreatedAt:      fixedRefundNow().Format(time.RFC3339Nano),
	}
}

func refundTestReceipt(requestID string, claimID, cost int64) *CostReceipt {
	return &CostReceipt{
		RequestID:           requestID,
		TenantID:            9,
		ClaimID:             claimID,
		Model:               "gpt-4o",
		InputTokens:         100,
		OutputTokens:        20,
		CostUSDMicros:       cost,
		RateTableSnapshotID: 12,
		ValidationState:     ReceiptValidationStateValid,
		Verdict:             ReceiptVerdictMatch,
		CreatedAt:           fixedRefundNow(),
	}
}

func fixedRefundNow() time.Time {
	return time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
}

type recordingMismatchRefundQueue struct {
	event dlq.Event
}

func (q *recordingMismatchRefundQueue) Enqueue(_ context.Context, event dlq.Event) (int64, error) {
	q.event = event
	return 77, nil
}

type recordingRefundSettler struct {
	refundCalls int
	lastRefund  billing.RefundRequest
	refundErr   error
}

func (s *recordingRefundSettler) Settle(context.Context, billing.SettleRequest) (*billing.SettleResult, error) {
	return &billing.SettleResult{}, nil
}

func (s *recordingRefundSettler) Abort(context.Context, int64, int64, string, string) error {
	return nil
}

func (s *recordingRefundSettler) Refund(_ context.Context, req billing.RefundRequest) (*billing.RefundResult, error) {
	s.refundCalls++
	s.lastRefund = req
	if s.refundErr != nil {
		return nil, s.refundErr
	}
	return &billing.RefundResult{
		RefundMicroUSD: req.AmountMicroUSD,
		BillingEventID: 123,
		AdjustmentRef:  "billing_event:123",
	}, nil
}

type recordingRefundReceiptSink struct {
	receipt            *CostReceipt
	receipts           []*CostReceipt
	idempotencyLookups int
}

func (s *recordingRefundReceiptSink) AppendRefundReceipt(_ context.Context, receipt *CostReceipt) error {
	clone := cloneReceipt(receipt)
	s.receipt = clone
	s.receipts = append(s.receipts, clone)
	return nil
}

func (s *recordingRefundReceiptSink) MaxReceiptSequence(_ context.Context, requestID string, tenantID int64) (int32, error) {
	var maxSequence int32
	for _, receipt := range s.receipts {
		if receipt.RequestID == requestID && receipt.TenantID == tenantID && receipt.ReceiptSequence > maxSequence {
			maxSequence = receipt.ReceiptSequence
		}
	}
	return maxSequence, nil
}

func (s *recordingRefundReceiptSink) GetByRefundIdempotency(_ context.Context, requestID string, tenantID int64, idempotencyKey string) (*CostReceipt, error) {
	s.idempotencyLookups++
	for _, receipt := range s.receipts {
		if receipt.RequestID == requestID && receipt.TenantID == tenantID && receiptHasAdjustmentRef(receipt, idempotencyKey) {
			return cloneReceipt(receipt), nil
		}
	}
	return nil, ErrReceiptNotFound
}

type duplicateRefundLedger struct {
	entry       auditledger.LedgerEntry
	appendCalls int
	lookupCalls int
}

func (l *duplicateRefundLedger) Append(context.Context, auditledger.LedgerEntry) (auditledger.LedgerEntry, error) {
	l.appendCalls++
	return auditledger.LedgerEntry{}, fmt.Errorf("auditledger: insert: %w", &pgconn.PgError{
		Code:    "23505",
		Message: "duplicate key value violates unique constraint",
	})
}

func (l *duplicateRefundLedger) GetByRequestID(_ context.Context, requestID string) (auditledger.LedgerEntry, error) {
	l.lookupCalls++
	if l.entry.RequestID != requestID {
		return auditledger.LedgerEntry{}, auditledger.ErrLedgerEntryNotFound
	}
	return l.entry, nil
}

func (l *duplicateRefundLedger) LatestMerkleRoot(context.Context) ([32]byte, error) {
	return auditledger.ZeroRoot, nil
}

func (l *duplicateRefundLedger) Size(context.Context) int {
	return 1
}

type duplicateRefundReceiptSink struct {
	existing    *CostReceipt
	appendCalls int
	lookupCalls int
}

func (s *duplicateRefundReceiptSink) AppendRefundReceipt(context.Context, *CostReceipt) error {
	s.appendCalls++
	return fmt.Errorf("audit: receipt retry duplicate: %w", ErrReceiptDuplicate)
}

func (s *duplicateRefundReceiptSink) GetReceiptBySequence(_ context.Context, requestID string, tenantID int64, sequence int32) (*CostReceipt, error) {
	s.lookupCalls++
	if s.existing == nil ||
		s.existing.RequestID != requestID ||
		s.existing.TenantID != tenantID ||
		s.existing.ReceiptSequence != sequence {
		return nil, ErrReceiptNotFound
	}
	return cloneReceipt(s.existing), nil
}

func (s *duplicateRefundReceiptSink) GetByRefundIdempotency(_ context.Context, requestID string, tenantID int64, idempotencyKey string) (*CostReceipt, error) {
	s.lookupCalls++
	if s.existing == nil ||
		s.existing.RequestID != requestID ||
		s.existing.TenantID != tenantID ||
		!receiptHasAdjustmentRef(s.existing, idempotencyKey) {
		return nil, ErrReceiptNotFound
	}
	return cloneReceipt(s.existing), nil
}

func receiptHasAdjustmentRef(receipt *CostReceipt, want string) bool {
	if receipt == nil {
		return false
	}
	for _, ref := range receipt.AdjustmentRefs {
		if ref == want {
			return true
		}
	}
	return false
}
