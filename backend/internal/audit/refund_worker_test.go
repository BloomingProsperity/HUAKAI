package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
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

	verifier := &recordingRefundEligibilityVerifier{}
	queue := NewMismatchRefundQueue(&recordingMismatchRefundQueue{},
		WithRefundEligibilityVerifier(verifier),
		WithRefundNow(fixedRefundNow))
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
	if verifier.calls != 1 || verifier.request.ClaimID != derived.ClaimID || verifier.request.AmountMicroUSD != verdict.DeltaMicroUSD {
		t.Fatalf("eligibility calls=%d request=%+v", verifier.calls, verifier.request)
	}
}

func TestMismatchRefundQueueDoesNotEnqueueWithoutCapturedCharge(t *testing.T) {
	queueStore := &recordingMismatchRefundQueue{}
	queue := NewMismatchRefundQueue(queueStore,
		WithRefundEligibilityVerifier(&recordingRefundEligibilityVerifier{err: billing.ErrRefundNoCapturedCharge}),
		WithRefundNow(fixedRefundNow))
	receipt := refundTestReceipt("req-unpaid-no-enqueue", 1002, 200)
	verdict := MismatchVerdict{State: ReceiptValidationStateMismatchPending, DeltaMicroUSD: 40, MismatchDirection: MismatchDirectionOverCharge}

	_, err := queue.EnqueueMismatchRefund(context.Background(), receipt, verdict)
	if !errors.Is(err, billing.ErrRefundNoCapturedCharge) {
		t.Fatalf("enqueue error=%v want ErrRefundNoCapturedCharge", err)
	}
	if queueStore.event.EventKind != "" {
		t.Fatalf("unpaid mismatch must not enqueue event: %+v", queueStore.event)
	}
}

func TestMismatchRefundQueueRejectsUnderChargeBeforeEligibilityCheck(t *testing.T) {
	queueStore := &recordingMismatchRefundQueue{}
	verifier := &recordingRefundEligibilityVerifier{}
	queue := NewMismatchRefundQueue(queueStore,
		WithRefundEligibilityVerifier(verifier),
		WithRefundNow(fixedRefundNow))
	receipt := refundTestReceipt("req-under-charge-no-refund", 1003, 240)
	verdict := MismatchVerdict{
		State:             ReceiptValidationStateMismatchPending,
		DeltaMicroUSD:     40,
		MismatchDirection: MismatchDirectionUnderCharge,
	}

	_, err := queue.EnqueueMismatchRefund(context.Background(), receipt, verdict)
	if !errors.Is(err, ErrReceiptInvalidDerivedData) {
		t.Fatalf("enqueue error=%v want ErrReceiptInvalidDerivedData", err)
	}
	if verifier.calls != 0 {
		t.Fatalf("under-charge reached money eligibility calls=%d", verifier.calls)
	}
	if queueStore.event.EventKind != "" {
		t.Fatalf("under-charge must not enqueue event: %+v", queueStore.event)
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
		settler.lastRefund.Reason != AuditMismatchRefundReason ||
		settler.lastRefund.IdempotencyKey != mismatchRefundIdempotencyKey(payload.ClaimID) ||
		settler.lastRefund.AuditRequestID != refundAuditRequestID(payload.RequestID, payload.ClaimID) ||
		!settler.lastRefund.RequireExact {
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
	if settler.refundCalls != 2 {
		t.Fatalf("refund calls=%d want 2（第二次必须重新核对账务幂等事实）", settler.refundCalls)
	}
	if got := store.Status(payload.ClaimID); got != "completed" {
		t.Fatalf("pending status=%q want completed", got)
	}
}

func TestRefundCompletedStatusWithoutEvidenceRebuildsRefundReceipt(t *testing.T) {
	ctx := context.Background()
	worker, settler, store, sink, payload := refundWorkerFixture(t, nil)
	if _, err := store.EnsurePending(ctx, payload); err != nil {
		t.Fatalf("EnsurePending: %v", err)
	}
	store.mu.Lock()
	rec := store.rows[payload.ClaimID]
	rec.Status = "completed"
	store.rows[payload.ClaimID] = rec
	store.mu.Unlock()

	if err := worker.Apply(ctx, payload); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if settler.refundCalls != 1 {
		t.Fatalf("refund calls=%d want 1", settler.refundCalls)
	}
	if sink.receipt == nil || sink.receipt.ClaimID != payload.ClaimID {
		t.Fatalf("refund receipt=%+v", sink.receipt)
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

func TestRefundNoopCannotCompletePendingOrSignReceipt(t *testing.T) {
	worker, settler, store, sink, payload := refundWorkerFixture(t, nil)
	settler.refundResult = &billing.RefundResult{AdjustmentRef: billing.RefundSkippedAmountZeroRef}

	err := worker.Apply(context.Background(), payload)
	if !errors.Is(err, billing.ErrRefundAmountNotCovered) {
		t.Fatalf("Apply error=%v want ErrRefundAmountNotCovered", err)
	}
	if got := store.Status(payload.ClaimID); got != "failed" {
		t.Fatalf("pending status=%q want failed", got)
	}
	if sink.receipt != nil {
		t.Fatalf("zero refund must not sign refunded receipt: %+v", sink.receipt)
	}
}

func TestRefundAlreadySatisfiedCompletesWithPriorAdjustment(t *testing.T) {
	worker, settler, store, sink, payload := refundWorkerFixture(t, nil)
	settler.refundResult = &billing.RefundResult{
		BillingEventID:   456,
		AdjustmentRef:    "billing_event:456",
		CoveredMicroUSD:  payload.DeltaMicroUSD,
		AlreadySatisfied: true,
	}

	if err := worker.Apply(context.Background(), payload); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := store.Status(payload.ClaimID); got != "completed" {
		t.Fatalf("pending status=%q want completed", got)
	}
	if sink.receipt == nil || !receiptHasAdjustmentRef(sink.receipt, "billing_event:456") {
		t.Fatalf("satisfied refund receipt missing prior adjustment: %+v", sink.receipt)
	}
}

func TestMismatchRefundHandlerRejectsConflictingDLQMetadata(t *testing.T) {
	_, _, _, _, payload := refundWorkerFixture(t, nil)
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	newRecord := func() dlq.Record {
		claimID := payload.ClaimID
		sourceID := payload.ClaimID
		return dlq.Record{
			TenantID:       payload.TenantID,
			ClaimID:        &claimID,
			Payload:        raw,
			EventKind:      dlq.EventKindAuditMismatchRefund,
			IdempotencyKey: mismatchRefundIdempotencyKey(payload.ClaimID),
			SourceTable:    "audit_refund_pending",
			SourceID:       &sourceID,
		}
	}
	tests := []struct {
		name   string
		mutate func(*dlq.Record)
	}{
		{name: "event_kind", mutate: func(rec *dlq.Record) { rec.EventKind = dlq.EventKindMetrics }},
		{name: "tenant", mutate: func(rec *dlq.Record) { rec.TenantID++ }},
		{name: "claim", mutate: func(rec *dlq.Record) { *rec.ClaimID++ }},
		{name: "source_table", mutate: func(rec *dlq.Record) { rec.SourceTable = "other" }},
		{name: "source_id", mutate: func(rec *dlq.Record) { *rec.SourceID++ }},
		{name: "idempotency", mutate: func(rec *dlq.Record) { rec.IdempotencyKey = "other" }},
		{name: "payload", mutate: func(rec *dlq.Record) { rec.Payload = json.RawMessage(`{"tenant_id":`) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worker, settler, _, _, _ := refundWorkerFixture(t, nil)
			rec := newRecord()
			tt.mutate(&rec)
			err := worker.Handle(context.Background(), rec)
			if !errors.Is(err, dlq.ErrUnretryable) {
				t.Fatalf("Handle error=%v want ErrUnretryable", err)
			}
			if settler.refundCalls != 0 {
				t.Fatalf("conflicting metadata reached money path calls=%d", settler.refundCalls)
			}
		})
	}
}

func TestMismatchRefundHandlerQuarantinesStructuralBillingFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "没有真实扣款", err: billing.ErrRefundNoCapturedCharge},
		{name: "幂等请求冲突", err: billing.ErrRefundIdempotencyConflict},
		{name: "退款事实损坏", err: billing.ErrRefundFactInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worker, _, store, _, payload := refundWorkerFixture(t, tt.err)
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("marshal payload: %v", err)
			}
			claimID := payload.ClaimID
			sourceID := payload.ClaimID
			err = worker.Handle(context.Background(), dlq.Record{
				TenantID:       payload.TenantID,
				ClaimID:        &claimID,
				Payload:        raw,
				EventKind:      dlq.EventKindAuditMismatchRefund,
				IdempotencyKey: mismatchRefundIdempotencyKey(payload.ClaimID),
				SourceTable:    "audit_refund_pending",
				SourceID:       &sourceID,
			})
			if !errors.Is(err, dlq.ErrUnretryable) || !errors.Is(err, tt.err) {
				t.Fatalf("Handle error=%v want unretryable %v", err, tt.err)
			}
			if got := store.Status(payload.ClaimID); got != "failed" {
				t.Fatalf("pending status=%q want failed", got)
			}
		})
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
			ModelChain: &proto.ModelChain{
				Requested:        "audit_mismatch_refund",
				RouteDecided:     "audit_mismatch_refund",
				UpstreamReported: "audit_mismatch_refund",
				Verdict:          "mismatch",
			},
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

func TestRefundLedgerDuplicateRejectsDifferentTenant(t *testing.T) {
	ctx := context.Background()
	worker, _, store, sink, payload := refundWorkerFixture(t, nil)
	worker.ledger = &duplicateRefundLedger{
		entry: auditledger.LedgerEntry{
			LedgerID:  "ldg_refund_wrong_tenant",
			RequestID: refundAuditRequestID(payload.RequestID, payload.ClaimID),
			TenantID:  payload.TenantID + 1,
			ModelChain: &proto.ModelChain{
				Requested:        "audit_mismatch_refund",
				RouteDecided:     "audit_mismatch_refund",
				UpstreamReported: "audit_mismatch_refund",
				Verdict:          "mismatch",
			},
		},
	}

	err := worker.Apply(ctx, payload)
	if !errors.Is(err, ErrReceiptInvalidDerivedData) {
		t.Fatalf("Apply error=%v want ErrReceiptInvalidDerivedData", err)
	}
	if got := store.Status(payload.ClaimID); got != "failed" {
		t.Fatalf("pending status=%q want failed", got)
	}
	if sink.receipt != nil {
		t.Fatalf("conflicting ledger reached receipt append: %+v", sink.receipt)
	}
}

func TestAT_AUDIT_001_034_RefundReceiptAppendDuplicateRetryCompletes(t *testing.T) {
	ctx := context.Background()
	worker, settler, store, _, payload := refundWorkerFixture(t, nil)
	existing := refundTestReceipt(payload.RequestID, payload.ClaimID, 240)
	existing.TenantID = payload.TenantID
	existing.ReceiptSequence = 1
	existing.ValidationState = ReceiptValidationStateMismatchRefunded
	existing.Verdict = ReceiptVerdictMismatchRefundPending
	existing.AdjustmentRefs = []string{
		refundReceiptIdempotencyKey(payload),
		"billing_event:123",
		"audit_ledger:ldg_t9_2",
	}
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
	if settler.refundCalls != 1 {
		t.Fatalf("existing receipt must still verify billing facts, refund calls=%d want 1", settler.refundCalls)
	}
	if sink.appendCalls != 0 || sink.lookupCalls != 2 {
		t.Fatalf("receipt sink calls append=%d lookup=%d", sink.appendCalls, sink.lookupCalls)
	}
}

func TestRefundExistingReceiptRejectsDifferentClaim(t *testing.T) {
	ctx := context.Background()
	worker, settler, store, _, payload := refundWorkerFixture(t, nil)
	existing := refundTestReceipt(payload.RequestID, payload.ClaimID+1, 240)
	existing.TenantID = payload.TenantID
	existing.ReceiptSequence = 1
	existing.ValidationState = ReceiptValidationStateMismatchRefunded
	existing.Verdict = ReceiptVerdictMismatchRefundPending
	existing.AdjustmentRefs = []string{refundReceiptIdempotencyKey(payload)}
	existing.SignerFingerprint = []byte("existing-fingerprint")
	existing.SignedHash = []byte("existing-signature")
	worker.receiptSink = &duplicateRefundReceiptSink{existing: existing}

	err := worker.Apply(ctx, payload)
	if !errors.Is(err, ErrReceiptInvalidDerivedData) {
		t.Fatalf("Apply error=%v want ErrReceiptInvalidDerivedData", err)
	}
	if settler.refundCalls != 0 {
		t.Fatalf("conflicting receipt reached money path calls=%d", settler.refundCalls)
	}
	if got := store.Status(payload.ClaimID); got != "failed" {
		t.Fatalf("pending status=%q want failed", got)
	}
}

func TestRefundCompletedReceiptRejectsMissingBillingEvidence(t *testing.T) {
	ctx := context.Background()
	worker, settler, store, _, payload := refundWorkerFixture(t, nil)
	existing := refundTestReceipt(payload.RequestID, payload.ClaimID, 240)
	existing.ReceiptSequence = 1
	existing.ValidationState = ReceiptValidationStateMismatchRefunded
	existing.Verdict = ReceiptVerdictMismatchRefundPending
	existing.AdjustmentRefs = []string{refundReceiptIdempotencyKey(payload)}
	existing.SignerFingerprint = []byte("existing-fingerprint")
	existing.SignedHash = []byte("existing-signature")
	worker.receiptSink = &duplicateRefundReceiptSink{existing: existing}

	err := worker.Apply(ctx, payload)
	if !errors.Is(err, ErrReceiptInvalidDerivedData) {
		t.Fatalf("Apply error=%v want ErrReceiptInvalidDerivedData", err)
	}
	if settler.refundCalls != 0 {
		t.Fatalf("incomplete receipt evidence reached money path calls=%d", settler.refundCalls)
	}
	if got := store.Status(payload.ClaimID); got != "failed" {
		t.Fatalf("pending status=%q want failed", got)
	}
}

func TestRefundFailureIncludesPendingStateWriteFailure(t *testing.T) {
	refundErr := errors.New("refund unavailable")
	markErr := errors.New("pending state unavailable")
	worker, _, store, _, payload := refundWorkerFixture(t, refundErr)
	worker.pending = &markFailedErrorRefundPendingStore{RefundPendingStore: store, err: markErr}

	err := worker.Apply(context.Background(), payload)
	if !errors.Is(err, refundErr) || !errors.Is(err, markErr) {
		t.Fatalf("Apply error=%v want both failures", err)
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
	if settler.refundCalls != 2 {
		t.Fatalf("refund calls=%d want 2（恢复必须重放账务核验）", settler.refundCalls)
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

func TestRefundExistingReceiptRejectsUnrelatedBillingEvent(t *testing.T) {
	ctx := context.Background()
	worker, settler, store, _, payload := refundWorkerFixture(t, nil)
	existing := refundTestReceipt(payload.RequestID, payload.ClaimID, 240)
	existing.TenantID = payload.TenantID
	existing.ReceiptSequence = 7
	existing.ValidationState = ReceiptValidationStateMismatchRefunded
	existing.Verdict = ReceiptVerdictMismatchRefundPending
	existing.AdjustmentRefs = []string{refundReceiptIdempotencyKey(payload), "billing_event:999"}
	existing.SignerFingerprint = []byte("existing-fingerprint")
	existing.SignedHash = []byte("existing-signature")
	sink := &duplicateRefundReceiptSink{existing: existing}
	worker.receiptSink = sink

	err := worker.Apply(ctx, payload)
	if !errors.Is(err, ErrReceiptInvalidDerivedData) {
		t.Fatalf("Apply error=%v want ErrReceiptInvalidDerivedData", err)
	}
	if got := store.Status(payload.ClaimID); got != "failed" {
		t.Fatalf("pending status=%q want failed", got)
	}
	if settler.refundCalls != 1 {
		t.Fatalf("refund calls=%d want 1（必须先取得真实账单引用）", settler.refundCalls)
	}
	if sink.appendCalls != 0 || sink.lookupCalls != 2 {
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

func refundWorkerFixture(t *testing.T, refundErr error) (*MismatchRefundWorker, *recordingRefundSettler, *memoryRefundPendingStore, *recordingRefundReceiptSink, MismatchRefundPayload) {
	t.Helper()
	ctx := context.Background()
	requestID := "req-refund-worker"
	signer := testAuditSigner(t, 51)
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if _, err := ledger.Append(ctx, mustPrepareAuditLedgerEntry(t, ctx, auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  9,
		ModelChain: &proto.ModelChain{
			Requested:        "gpt-4o",
			RouteDecided:     "gpt-4o",
			UpstreamReported: "gpt-4o",
			Verdict:          "mismatch",
		},
	})); err != nil {
		t.Fatalf("append ledger: %v", err)
	}
	formatter, err := NewReceiptFormatter(ledger, &staticReceiptSource{inputs: ReceiptInputs{
		TenantID:            9,
		UserID:              7001,
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
	store := newMemoryRefundPendingStore()
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

type memoryRefundPendingStore struct {
	mu   sync.Mutex
	rows map[int64]RefundPendingRecord
}

func newMemoryRefundPendingStore() *memoryRefundPendingStore {
	return &memoryRefundPendingStore{rows: map[int64]RefundPendingRecord{}}
}

func (s *memoryRefundPendingStore) EnsurePending(_ context.Context, payload MismatchRefundPayload) (RefundPendingRecord, error) {
	if err := validateRefundPayload(payload); err != nil {
		return RefundPendingRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.rows[payload.ClaimID]
	if !exists {
		record = RefundPendingRecord{
			ClaimID:       payload.ClaimID,
			RequestID:     payload.RequestID,
			DeltaMicroUSD: payload.DeltaMicroUSD,
			Status:        "pending",
		}
		s.rows[payload.ClaimID] = record
		return record, nil
	}
	if strings.TrimSpace(record.RequestID) != strings.TrimSpace(payload.RequestID) || record.DeltaMicroUSD != payload.DeltaMicroUSD {
		return RefundPendingRecord{}, fmt.Errorf("%w: conflicting refund pending identity", ErrReceiptInvalidDerivedData)
	}
	record.Status = "pending"
	s.rows[payload.ClaimID] = record
	return record, nil
}

func (s *memoryRefundPendingStore) MarkCompleted(_ context.Context, claimID int64, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.rows[claimID]
	record.Status = "completed"
	s.rows[claimID] = record
	return nil
}

func (s *memoryRefundPendingStore) MarkFailed(_ context.Context, claimID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.rows[claimID]
	if record.Status != "completed" {
		record.Status = "failed"
		s.rows[claimID] = record
	}
	return nil
}

func (s *memoryRefundPendingStore) Status(claimID int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rows[claimID].Status
}

func refundTestReceipt(requestID string, claimID, cost int64) *CostReceipt {
	return &CostReceipt{
		RequestID:           requestID,
		TenantID:            9,
		UserID:              7001,
		ClaimID:             claimID,
		OwnerSource:         ReceiptOwnerSourceSettle,
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

type recordingRefundEligibilityVerifier struct {
	calls   int
	request billing.RefundRequest
	err     error
}

func (v *recordingRefundEligibilityVerifier) VerifyRefundableCharge(_ context.Context, req billing.RefundRequest) error {
	v.calls++
	v.request = req
	return v.err
}

type recordingRefundSettler struct {
	refundCalls  int
	lastRefund   billing.RefundRequest
	refundErr    error
	refundResult *billing.RefundResult
	refunds      map[string]billing.RefundResult
}

func (s *recordingRefundSettler) Settle(context.Context, billing.SettleRequest) (*billing.SettleResult, error) {
	return &billing.SettleResult{}, nil
}

func (s *recordingRefundSettler) Abort(context.Context, int64, int64, string, string, int64, json.RawMessage) error {
	return nil
}

func (s *recordingRefundSettler) CommitCacheHit(context.Context, billing.SettleRequest) error {
	return nil
}

func (s *recordingRefundSettler) Refund(_ context.Context, req billing.RefundRequest) (*billing.RefundResult, error) {
	s.refundCalls++
	s.lastRefund = req
	if s.refundErr != nil {
		return nil, s.refundErr
	}
	if s.refundResult != nil {
		return s.refundResult, nil
	}
	if stored, ok := s.refunds[req.IdempotencyKey]; ok {
		stored.Idempotent = true
		stored.BalanceCredited = false
		return &stored, nil
	}
	result := billing.RefundResult{
		RefundMicroUSD:  req.AmountMicroUSD,
		BillingEventID:  123,
		AdjustmentRef:   "billing_event:123",
		CoveredMicroUSD: req.AmountMicroUSD,
		BalanceCredited: true,
	}
	if s.refunds == nil {
		s.refunds = make(map[string]billing.RefundResult)
	}
	s.refunds[req.IdempotencyKey] = result
	return &result, nil
}

type recordingRefundReceiptSink struct {
	receipt            *CostReceipt
	receipts           []*CostReceipt
	idempotencyLookups int
}

type markFailedErrorRefundPendingStore struct {
	RefundPendingStore
	err error
}

func (s *markFailedErrorRefundPendingStore) MarkFailed(context.Context, int64) error {
	return s.err
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

func (l *duplicateRefundLedger) Append(context.Context, auditledger.PreparedEntry) (auditledger.LedgerEntry, error) {
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

func (l *duplicateRefundLedger) GetByRequestIDAndTenantScope(_ context.Context, requestID, tenantScopeRef string) (auditledger.LedgerEntry, error) {
	return l.GetByRequestID(context.Background(), requestID)
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
