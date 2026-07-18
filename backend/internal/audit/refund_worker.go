package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

const AuditMismatchRefundReason = "audit_mismatch_v1"

type RefundReceiptSink interface {
	AppendRefundReceipt(context.Context, *CostReceipt) error
}

type refundTransactionBeginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

type refundSettlerInTx interface {
	RefundInTx(context.Context, pgx.Tx, billing.RefundRequest) (*billing.RefundResult, error)
}

type refundLedgerInTx interface {
	AppendInTx(context.Context, pgx.Tx, auditledger.PreparedEntry) (auditledger.LedgerEntry, error)
}

type refundReceiptSinkInTx interface {
	AppendRefundReceiptInTx(context.Context, pgx.Tx, *CostReceipt) error
}

type refundReceiptLatestReader interface {
	GetReceiptForAdmin(context.Context, string, int64) (*CostReceipt, error)
}

type refundReceiptIdempotencyReader interface {
	GetByRefundIdempotency(context.Context, string, int64, string) (*CostReceipt, error)
}

type refundReceiptMaxSequenceReader interface {
	MaxReceiptSequence(context.Context, string, int64) (int32, error)
}

type refundReceiptMaxSequenceReaderInTx interface {
	MaxReceiptSequenceInTx(context.Context, pgx.Tx, string, int64) (int32, error)
}

type MismatchRefundWorker struct {
	pending       RefundPendingStore
	settler       billing.Settler
	formatter     receiptFormatterService
	ledger        auditledger.Ledger
	receiptSink   RefundReceiptSink
	txBeginner    refundTransactionBeginner
	quotaReverser QuotaReverser
	now           func() time.Time
}

type RefundWorkerOption struct {
	applyWorker func(*MismatchRefundWorker)
	applyQueue  func(*MismatchRefundQueue)
}

func WithRefundLedger(ledger auditledger.Ledger) RefundWorkerOption {
	return RefundWorkerOption{applyWorker: func(w *MismatchRefundWorker) { w.ledger = ledger }}
}

func WithRefundReceiptSink(sink RefundReceiptSink) RefundWorkerOption {
	return RefundWorkerOption{applyWorker: func(w *MismatchRefundWorker) { w.receiptSink = sink }}
}

func WithRefundTxPool(pool *pgxpool.Pool) RefundWorkerOption {
	return RefundWorkerOption{applyWorker: func(w *MismatchRefundWorker) {
		if pool != nil {
			w.txBeginner = pool
		}
	}}
}

func WithRefundNow(now func() time.Time) RefundWorkerOption {
	return RefundWorkerOption{
		applyWorker: func(w *MismatchRefundWorker) {
			if now != nil {
				w.now = now
			}
		},
		applyQueue: func(q *MismatchRefundQueue) {
			if now != nil {
				q.now = now
			}
		},
	}
}

func WithRefundEligibilityVerifier(verifier MismatchRefundEligibilityVerifier) RefundWorkerOption {
	return RefundWorkerOption{applyQueue: func(q *MismatchRefundQueue) {
		q.eligibility = verifier
	}}
}

func NewMismatchRefundWorker(pending RefundPendingStore, settler billing.Settler, formatter receiptFormatterService, opts ...RefundWorkerOption) *MismatchRefundWorker {
	w := &MismatchRefundWorker{
		pending:   pending,
		settler:   settler,
		formatter: formatter,
		now:       func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		if opt.applyWorker != nil {
			opt.applyWorker(w)
		}
	}
	return w
}

func (w *MismatchRefundWorker) Handler() dlq.Handler {
	return func(ctx context.Context, rec dlq.Record) error {
		return w.Handle(ctx, rec)
	}
}

func (w *MismatchRefundWorker) Handle(ctx context.Context, rec dlq.Record) error {
	if w == nil || w.pending == nil || w.settler == nil {
		return dlq.ErrStoreNotConfigured
	}
	if rec.EventKind != dlq.EventKindAuditMismatchRefund {
		return fmt.Errorf("%w: audit refund event kind mismatch", dlq.ErrUnretryable)
	}
	var payload MismatchRefundPayload
	if err := json.Unmarshal(rec.Payload, &payload); err != nil {
		return fmt.Errorf("%w: audit: decode mismatch refund payload: %v", dlq.ErrUnretryable, err)
	}
	if payload.TenantID == 0 {
		payload.TenantID = rec.TenantID
	} else if rec.TenantID != payload.TenantID {
		return fmt.Errorf("%w: audit refund tenant metadata mismatch", dlq.ErrUnretryable)
	}
	if payload.ClaimID == 0 && rec.ClaimID != nil {
		payload.ClaimID = *rec.ClaimID
	} else if rec.ClaimID == nil || *rec.ClaimID != payload.ClaimID {
		return fmt.Errorf("%w: audit refund claim metadata mismatch", dlq.ErrUnretryable)
	}
	if rec.SourceTable != "audit_refund_pending" || rec.SourceID == nil || *rec.SourceID != payload.ClaimID ||
		strings.TrimSpace(rec.IdempotencyKey) != mismatchRefundIdempotencyKey(payload.ClaimID) {
		return fmt.Errorf("%w: audit refund source metadata mismatch", dlq.ErrUnretryable)
	}
	if err := w.Apply(ctx, payload); err != nil {
		return classifyMismatchRefundHandlerError(err)
	}
	return nil
}

func (w *MismatchRefundWorker) Apply(ctx context.Context, payload MismatchRefundPayload) error {
	if err := validateRefundPayload(payload); err != nil {
		return err
	}
	rec, err := w.pending.EnsurePending(ctx, payload)
	if err != nil {
		return err
	}
	if rec.ClaimID != payload.ClaimID || strings.TrimSpace(rec.RequestID) != strings.TrimSpace(payload.RequestID) ||
		rec.DeltaMicroUSD != payload.DeltaMicroUSD {
		return fmt.Errorf("%w: refund pending payload mismatch", ErrReceiptInvalidDerivedData)
	}
	// 已有收据只能作为恢复线索，不能单独证明退款已经落账。先拦住身份或结构损坏的
	// 收据，随后仍由账务幂等事实返回真实 billing event，再做精确引用核对。
	if err := w.validateExistingRefundReceiptBeforeMoney(ctx, payload); err != nil {
		return w.failPending(ctx, payload.ClaimID, err)
	}
	if w.txBeginner != nil {
		return w.applyInTx(ctx, payload)
	}
	return w.applyLegacy(ctx, payload)
}

func (w *MismatchRefundWorker) applyLegacy(ctx context.Context, payload MismatchRefundPayload) error {
	refund, err := w.settler.Refund(ctx, billing.RefundRequest{
		TenantID:       payload.TenantID,
		ClaimID:        payload.ClaimID,
		AmountMicroUSD: payload.DeltaMicroUSD,
		Reason:         AuditMismatchRefundReason,
		IdempotencyKey: mismatchRefundIdempotencyKey(payload.ClaimID),
		AuditRequestID: refundAuditRequestID(payload.RequestID, payload.ClaimID),
		RequireExact:   true,
	})
	if err != nil {
		return w.failPending(ctx, payload.ClaimID, err)
	}
	if err := validateAppliedRefund(refund, payload.DeltaMicroUSD); err != nil {
		return w.failPending(ctx, payload.ClaimID, err)
	}

	refs := []string{}
	if refund != nil && strings.TrimSpace(refund.AdjustmentRef) != "" {
		refs = append(refs, strings.TrimSpace(refund.AdjustmentRef))
	}
	if ledgerRef, err := w.appendRefundLedger(ctx, payload); err != nil {
		return w.failPending(ctx, payload.ClaimID, err)
	} else if ledgerRef != "" {
		refs = append(refs, ledgerRef)
	}
	if err := w.signRefundedReceipt(ctx, payload, refs); err != nil {
		return w.failPending(ctx, payload.ClaimID, err)
	}
	w.reverseQuotaAfterLegacyRefund(ctx, payload, refund)
	return w.pending.MarkCompleted(ctx, payload.ClaimID, w.now())
}

func (w *MismatchRefundWorker) applyInTx(ctx context.Context, payload MismatchRefundPayload) error {
	settler, ok := w.settler.(refundSettlerInTx)
	if !ok {
		return fmt.Errorf("audit: refund settler does not support transaction-bound refund")
	}
	var ledger refundLedgerInTx
	if w.ledger != nil {
		var ledgerOK bool
		ledger, ledgerOK = w.ledger.(refundLedgerInTx)
		if !ledgerOK {
			return fmt.Errorf("audit: refund ledger does not support transaction-bound append")
		}
	}
	var receiptSink refundReceiptSinkInTx
	if w.receiptSink != nil {
		var receiptOK bool
		receiptSink, receiptOK = w.receiptSink.(refundReceiptSinkInTx)
		if !receiptOK {
			return fmt.Errorf("audit: refund receipt sink does not support transaction-bound append")
		}
	}
	var quotaReverser QuotaReverserInTx
	if w.quotaReverser != nil {
		var quotaOK bool
		quotaReverser, quotaOK = w.quotaReverser.(QuotaReverserInTx)
		if !quotaOK {
			return fmt.Errorf("audit: refund quota reverser does not support transaction-bound reversal")
		}
	}
	err := retryRefundTransaction(ctx, func(ctx context.Context) error {
		_, err := w.applyInTxOnce(ctx, payload, settler, ledger, receiptSink, quotaReverser)
		return err
	})
	if err != nil {
		return w.failPending(ctx, payload.ClaimID, err)
	}
	return w.pending.MarkCompleted(ctx, payload.ClaimID, w.now())
}

// applyInTxOnce 执行一轮完整的退款、审计账本与收据原子事务。调用方只在
// PostgreSQL 明确要求重放整个事务时重试，绝不单独重放其中一个资金副作用。
func (w *MismatchRefundWorker) applyInTxOnce(ctx context.Context, payload MismatchRefundPayload, settler refundSettlerInTx, ledger refundLedgerInTx, receiptSink refundReceiptSinkInTx, quotaReverser QuotaReverserInTx) (*billing.RefundResult, error) {
	tx, err := w.txBeginner.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("audit: begin refund transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	refs := []string{}
	refund, err := settler.RefundInTx(ctx, tx, billing.RefundRequest{
		TenantID:       payload.TenantID,
		ClaimID:        payload.ClaimID,
		AmountMicroUSD: payload.DeltaMicroUSD,
		Reason:         AuditMismatchRefundReason,
		IdempotencyKey: mismatchRefundIdempotencyKey(payload.ClaimID),
		AuditRequestID: refundAuditRequestID(payload.RequestID, payload.ClaimID),
		RequireExact:   true,
	})
	if err != nil {
		return nil, err
	}
	if err := validateAppliedRefund(refund, payload.DeltaMicroUSD); err != nil {
		return nil, err
	}
	if quotaReverser != nil && refund != nil && refund.RefundMicroUSD > 0 && !refund.Idempotent {
		if _, err := quotaReverser.ReverseSettledCostInTx(ctx, tx, payload.TenantID, payload.ClaimID, refund.RefundMicroUSD); err != nil {
			return nil, fmt.Errorf("audit: reverse quota in refund transaction: %w", err)
		}
	}
	if refund != nil && strings.TrimSpace(refund.AdjustmentRef) != "" {
		refs = append(refs, strings.TrimSpace(refund.AdjustmentRef))
	}
	if ledgerRef, err := w.appendRefundLedgerInTx(ctx, tx, ledger, payload); err != nil {
		return nil, err
	} else if ledgerRef != "" {
		refs = append(refs, ledgerRef)
	}
	if err := w.signRefundedReceiptInTx(ctx, tx, receiptSink, payload, refs); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("audit: commit refund transaction: %w", err)
	}
	committed = true
	return refund, nil
}

func (w *MismatchRefundWorker) appendRefundLedger(ctx context.Context, payload MismatchRefundPayload) (string, error) {
	if w == nil || w.ledger == nil {
		return "", nil
	}
	requestID := refundAuditRequestID(payload.RequestID, payload.ClaimID)
	rawEntry := auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  payload.TenantID,
		ModelChain: &proto.ModelChain{
			Requested:        "audit_mismatch_refund",
			RouteDecided:     "audit_mismatch_refund",
			UpstreamReported: "audit_mismatch_refund",
			Verdict:          "mismatch",
		},
	}
	prepared, err := auditledger.PrepareEntry(ctx, rawEntry)
	if err != nil {
		return "", fmt.Errorf("audit: prepare refund ledger entry: %w", err)
	}
	entry, err := w.ledger.Append(ctx, prepared)
	if isRefundLedgerDuplicate(err) {
		existing, getErr := w.ledger.GetByRequestID(ctx, requestID)
		if getErr != nil {
			return "", fmt.Errorf("audit: lookup duplicate refund ledger entry: %w", getErr)
		}
		validated, validateErr := validateRefundLedgerEntry(existing, payload)
		if validateErr != nil {
			return "", validateErr
		}
		return refundLedgerRef(requestID, validated), nil
	}
	if err != nil {
		return "", fmt.Errorf("audit: append refund ledger entry: %w", err)
	}
	validated, err := validateRefundLedgerEntry(entry, payload)
	if err != nil {
		return "", err
	}
	return refundLedgerRef(requestID, validated), nil
}

func (w *MismatchRefundWorker) appendRefundLedgerInTx(ctx context.Context, tx pgx.Tx, ledger refundLedgerInTx, payload MismatchRefundPayload) (string, error) {
	if w == nil || ledger == nil {
		return "", nil
	}
	requestID := refundAuditRequestID(payload.RequestID, payload.ClaimID)
	if existing, err := w.ledger.GetByRequestID(ctx, requestID); err == nil {
		validated, validateErr := validateRefundLedgerEntry(existing, payload)
		if validateErr != nil {
			return "", validateErr
		}
		return refundLedgerRef(requestID, validated), nil
	} else if !errors.Is(err, auditledger.ErrLedgerEntryNotFound) {
		return "", fmt.Errorf("audit: lookup existing refund ledger entry: %w", err)
	}
	rawEntry := auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  payload.TenantID,
		ModelChain: &proto.ModelChain{
			Requested:        "audit_mismatch_refund",
			RouteDecided:     "audit_mismatch_refund",
			UpstreamReported: "audit_mismatch_refund",
			Verdict:          "mismatch",
		},
	}
	prepared, err := auditledger.PrepareEntry(ctx, rawEntry)
	if err != nil {
		return "", fmt.Errorf("audit: prepare refund ledger entry: %w", err)
	}
	entry, err := ledger.AppendInTx(ctx, tx, prepared)
	if err != nil {
		return "", fmt.Errorf("audit: append refund ledger entry: %w", err)
	}
	validated, err := validateRefundLedgerEntry(entry, payload)
	if err != nil {
		return "", err
	}
	return refundLedgerRef(requestID, validated), nil
}

func (w *MismatchRefundWorker) signRefundedReceipt(ctx context.Context, payload MismatchRefundPayload, refs []string) error {
	if w == nil || w.formatter == nil {
		return nil
	}
	if existing, err := w.existingRefundReceiptByIdempotency(ctx, payload); err == nil {
		return validateRequiredRefundReceiptRefs(existing, refs)
	} else if !errors.Is(err, ErrReceiptNotFound) && !errors.Is(err, ErrReceiptStorageRequired) {
		return fmt.Errorf("audit: lookup existing refunded receipt: %w", err)
	}
	receipt, err := w.formatter.DeriveReceipt(ctx, payload.RequestID)
	if err != nil {
		return fmt.Errorf("audit: derive refunded receipt: %w", err)
	}
	receipt.ClaimID = payload.ClaimID
	sequence, err := w.nextRefundReceiptSequence(ctx, payload, receipt)
	if err != nil {
		return err
	}
	receipt.ReceiptSequence = sequence
	receipt.ValidationState = ReceiptValidationStateMismatchRefunded
	receipt.Verdict = ReceiptVerdictMismatchRefundPending
	receipt.AdjustmentRefs = refundReceiptAdjustmentRefs(payload, refs)
	signed, err := w.formatter.SignReceipt(ctx, receipt)
	if err != nil {
		return fmt.Errorf("audit: sign refunded receipt: %w", err)
	}
	if w.receiptSink != nil {
		if err := w.receiptSink.AppendRefundReceipt(ctx, signed); err != nil {
			if isRefundReceiptDuplicate(err) {
				existing, getErr := w.existingRefundReceiptByIdempotency(ctx, payload)
				if getErr != nil {
					return fmt.Errorf("audit: lookup duplicate refunded receipt: %w", getErr)
				}
				return validateRequiredRefundReceiptRefs(existing, refs)
			}
			return fmt.Errorf("audit: append refunded receipt: %w", err)
		}
	}
	return nil
}

func (w *MismatchRefundWorker) signRefundedReceiptInTx(ctx context.Context, tx pgx.Tx, sink refundReceiptSinkInTx, payload MismatchRefundPayload, refs []string) error {
	if w == nil || w.formatter == nil {
		return nil
	}
	if existing, err := w.existingRefundReceiptByIdempotency(ctx, payload); err == nil {
		return validateRequiredRefundReceiptRefs(existing, refs)
	} else if !errors.Is(err, ErrReceiptNotFound) && !errors.Is(err, ErrReceiptStorageRequired) {
		return fmt.Errorf("audit: lookup existing refunded receipt: %w", err)
	}
	receipt, err := w.formatter.DeriveReceipt(ctx, payload.RequestID)
	if err != nil {
		return fmt.Errorf("audit: derive refunded receipt: %w", err)
	}
	receipt.ClaimID = payload.ClaimID
	sequence, err := w.nextRefundReceiptSequenceInTx(ctx, tx, sink, payload, receipt)
	if err != nil {
		return err
	}
	receipt.ReceiptSequence = sequence
	receipt.ValidationState = ReceiptValidationStateMismatchRefunded
	receipt.Verdict = ReceiptVerdictMismatchRefundPending
	receipt.AdjustmentRefs = refundReceiptAdjustmentRefs(payload, refs)
	signed, err := w.formatter.SignReceipt(ctx, receipt)
	if err != nil {
		return fmt.Errorf("audit: sign refunded receipt: %w", err)
	}
	if sink != nil {
		if existing, err := w.existingRefundReceiptByIdempotency(ctx, payload); err == nil {
			return validateRequiredRefundReceiptRefs(existing, refs)
		} else if !errors.Is(err, ErrReceiptNotFound) && !errors.Is(err, ErrReceiptStorageRequired) {
			return fmt.Errorf("audit: lookup duplicate refunded receipt: %w", err)
		}
		if err := sink.AppendRefundReceiptInTx(ctx, tx, signed); err != nil {
			return fmt.Errorf("audit: append refunded receipt: %w", err)
		}
	}
	return nil
}

func (w *MismatchRefundWorker) validateExistingRefundReceiptBeforeMoney(ctx context.Context, payload MismatchRefundPayload) error {
	if w == nil {
		return nil
	}
	_, err := w.existingRefundReceiptByIdempotency(ctx, payload)
	if errors.Is(err, ErrReceiptNotFound) || errors.Is(err, ErrReceiptStorageRequired) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("audit: lookup existing refunded receipt: %w", err)
	}
	return nil
}

func (w *MismatchRefundWorker) nextRefundReceiptSequence(ctx context.Context, payload MismatchRefundPayload, receipt *CostReceipt) (int32, error) {
	base := receiptBaseSequence(receipt)
	if w == nil || w.receiptSink == nil {
		return base, nil
	}
	if reader, ok := w.receiptSink.(refundReceiptMaxSequenceReader); ok {
		return nextReceiptSequenceFromMax(ctx, reader, payload, base)
	}
	if reader, ok := w.receiptSink.(refundReceiptLatestReader); ok {
		return nextReceiptSequenceFromLatest(ctx, reader, payload, base)
	}
	return base, nil
}

func (w *MismatchRefundWorker) nextRefundReceiptSequenceInTx(ctx context.Context, tx pgx.Tx, sink refundReceiptSinkInTx, payload MismatchRefundPayload, receipt *CostReceipt) (int32, error) {
	base := receiptBaseSequence(receipt)
	if sink == nil {
		return base, nil
	}
	if reader, ok := sink.(refundReceiptMaxSequenceReaderInTx); ok {
		maxSequence, err := reader.MaxReceiptSequenceInTx(ctx, tx, payload.RequestID, payload.TenantID)
		if err != nil {
			return 0, fmt.Errorf("audit: lookup max refunded receipt sequence: %w", err)
		}
		return nextSequenceAfterMax(maxSequence, base)
	}
	if reader, ok := sink.(refundReceiptMaxSequenceReader); ok {
		return nextReceiptSequenceFromMax(ctx, reader, payload, base)
	}
	if reader, ok := sink.(refundReceiptLatestReader); ok {
		return nextReceiptSequenceFromLatest(ctx, reader, payload, base)
	}
	return base, nil
}

func receiptBaseSequence(receipt *CostReceipt) int32 {
	if receipt == nil || receipt.ReceiptSequence < 0 {
		return 1
	}
	if receipt.ReceiptSequence >= maxReceiptSequence {
		return maxReceiptSequence
	}
	return receipt.ReceiptSequence + 1
}

func nextReceiptSequenceFromMax(ctx context.Context, reader refundReceiptMaxSequenceReader, payload MismatchRefundPayload, base int32) (int32, error) {
	maxSequence, err := reader.MaxReceiptSequence(ctx, payload.RequestID, payload.TenantID)
	if err != nil {
		return 0, fmt.Errorf("audit: lookup max refunded receipt sequence: %w", err)
	}
	return nextSequenceAfterMax(maxSequence, base)
}

func nextReceiptSequenceFromLatest(ctx context.Context, reader refundReceiptLatestReader, payload MismatchRefundPayload, base int32) (int32, error) {
	latest, err := reader.GetReceiptForAdmin(ctx, payload.RequestID, payload.TenantID)
	if errors.Is(err, ErrReceiptNotFound) {
		return base, nil
	}
	if err != nil {
		return 0, fmt.Errorf("audit: lookup latest refunded receipt sequence: %w", err)
	}
	if latest == nil {
		return base, nil
	}
	return nextSequenceAfterMax(latest.ReceiptSequence, base)
}

const maxReceiptSequence int32 = 1<<31 - 1

func nextSequenceAfterMax(maxSequence, base int32) (int32, error) {
	if maxSequence < base {
		return base, nil
	}
	if maxSequence >= maxReceiptSequence {
		return 0, fmt.Errorf("%w: receipt_sequence overflow", ErrReceiptInvalidDerivedData)
	}
	return maxSequence + 1, nil
}

func (w *MismatchRefundWorker) existingRefundReceiptByIdempotency(ctx context.Context, payload MismatchRefundPayload) (*CostReceipt, error) {
	if w == nil || w.receiptSink == nil {
		return nil, ErrReceiptStorageRequired
	}
	reader, ok := w.receiptSink.(refundReceiptIdempotencyReader)
	if !ok {
		return nil, ErrReceiptStorageRequired
	}
	key := refundReceiptIdempotencyKey(payload)
	receipt, err := reader.GetByRefundIdempotency(ctx, payload.RequestID, payload.TenantID, key)
	if err != nil {
		return nil, err
	}
	return validateExistingRefundReceiptByIdempotency(receipt, payload, key)
}

func validateExistingRefundReceiptByIdempotency(receipt *CostReceipt, payload MismatchRefundPayload, key string) (*CostReceipt, error) {
	if receipt == nil {
		return nil, ErrReceiptNotFound
	}
	if strings.TrimSpace(receipt.RequestID) != strings.TrimSpace(payload.RequestID) ||
		receipt.TenantID != payload.TenantID ||
		receipt.ClaimID != payload.ClaimID {
		return nil, fmt.Errorf("%w: refunded receipt idempotency mismatch", ErrReceiptInvalidDerivedData)
	}
	if !adjustmentRefsContain(receipt.AdjustmentRefs, key) {
		return nil, fmt.Errorf("%w: refunded receipt idempotency ref mismatch", ErrReceiptInvalidDerivedData)
	}
	return validateRefundReceiptEvidence(receipt, payload)
}

func validateRefundReceiptEvidence(receipt *CostReceipt, payload MismatchRefundPayload) (*CostReceipt, error) {
	if NormalizeReceiptValidationState(receipt.ValidationState) != ReceiptValidationStateMismatchRefunded ||
		strings.TrimSpace(receipt.Verdict) != ReceiptVerdictMismatchRefundPending {
		return nil, fmt.Errorf("%w: refunded receipt state mismatch", ErrReceiptInvalidDerivedData)
	}
	if len(receipt.SignerFingerprint) == 0 || len(receipt.SignedHash) == 0 {
		return nil, fmt.Errorf("%w: refunded receipt signature evidence missing", ErrReceiptInvalidDerivedData)
	}
	if !adjustmentRefsContain(receipt.AdjustmentRefs, refundReceiptIdempotencyKey(payload)) ||
		!hasRefundBillingEventRef(receipt.AdjustmentRefs) {
		return nil, fmt.Errorf("%w: refunded receipt adjustment evidence missing", ErrReceiptInvalidDerivedData)
	}
	return receipt, nil
}

func hasRefundBillingEventRef(refs []string) bool {
	for _, ref := range refs {
		raw, ok := strings.CutPrefix(strings.TrimSpace(ref), "billing_event:")
		if !ok {
			continue
		}
		id, err := strconv.ParseInt(raw, 10, 64)
		if err == nil && id > 0 {
			return true
		}
	}
	return false
}

func validateRequiredRefundReceiptRefs(receipt *CostReceipt, required []string) error {
	if receipt == nil {
		return ErrReceiptNotFound
	}
	for _, ref := range required {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if !adjustmentRefsContain(receipt.AdjustmentRefs, ref) {
			return fmt.Errorf("%w: refunded receipt does not reference durable effect %s", ErrReceiptInvalidDerivedData, ref)
		}
	}
	return nil
}

func refundReceiptAdjustmentRefs(payload MismatchRefundPayload, refs []string) []string {
	out := make([]string, 0, len(refs)+1)
	key := refundReceiptIdempotencyKey(payload)
	if key != "" {
		out = append(out, key)
	}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" || adjustmentRefsContain(out, ref) {
			continue
		}
		out = append(out, ref)
	}
	return out
}

func refundReceiptIdempotencyKey(payload MismatchRefundPayload) string {
	return "refund_idempotency_key:" + mismatchRefundIdempotencyKey(payload.ClaimID)
}

func (w *MismatchRefundWorker) failPending(ctx context.Context, claimID int64, cause error) error {
	if w == nil || w.pending == nil {
		return cause
	}
	if err := w.pending.MarkFailed(ctx, claimID); err != nil {
		return errors.Join(cause, fmt.Errorf("audit: mark refund pending failed: %w", err))
	}
	return cause
}

func adjustmentRefsContain(refs []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, ref := range refs {
		if strings.TrimSpace(ref) == want {
			return true
		}
	}
	return false
}

func refundLedgerRef(requestID string, entry auditledger.LedgerEntry) string {
	if strings.TrimSpace(entry.LedgerID) != "" {
		return "audit_ledger:" + strings.TrimSpace(entry.LedgerID)
	}
	return "audit_ledger:" + strings.TrimSpace(requestID)
}

func validateRefundLedgerEntry(entry auditledger.LedgerEntry, payload MismatchRefundPayload) (auditledger.LedgerEntry, error) {
	expectedRequestID := refundAuditRequestID(payload.RequestID, payload.ClaimID)
	if strings.TrimSpace(entry.RequestID) != expectedRequestID || entry.TenantID != payload.TenantID {
		return auditledger.LedgerEntry{}, fmt.Errorf("%w: refund ledger identity mismatch", ErrReceiptInvalidDerivedData)
	}
	if entry.ModelChain == nil ||
		entry.ModelChain.Requested != "audit_mismatch_refund" ||
		entry.ModelChain.RouteDecided != "audit_mismatch_refund" ||
		entry.ModelChain.UpstreamReported != "audit_mismatch_refund" ||
		entry.ModelChain.Verdict != "mismatch" {
		return auditledger.LedgerEntry{}, fmt.Errorf("%w: refund ledger semantics mismatch", ErrReceiptInvalidDerivedData)
	}
	return entry, nil
}

func isRefundLedgerDuplicate(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, auditledger.ErrDuplicateRequestID) || isUniqueViolation(err)
}

func isRefundReceiptDuplicate(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrReceiptDuplicate) || isUniqueViolation(err)
}

func validateRefundPayload(payload MismatchRefundPayload) error {
	if payload.TenantID <= 0 || payload.ClaimID <= 0 || payload.DeltaMicroUSD <= 0 {
		return fmt.Errorf("%w: refund payload", ErrReceiptInvalidDerivedData)
	}
	return validateReceiptRequestID(payload.RequestID)
}

func validateAppliedRefund(refund *billing.RefundResult, expectedMicroUSD int64) error {
	if refund == nil {
		return errors.New("audit: refund returned nil result")
	}
	if expectedMicroUSD <= 0 || refund.CoveredMicroUSD < expectedMicroUSD {
		return billing.ErrRefundAmountNotCovered
	}
	if refund.AlreadySatisfied {
		if refund.BillingEventID <= 0 || strings.TrimSpace(refund.AdjustmentRef) == "" {
			return errors.New("audit: satisfied refund missing prior adjustment evidence")
		}
		return nil
	}
	if refund.RefundMicroUSD <= 0 || refund.BillingEventID <= 0 || strings.TrimSpace(refund.AdjustmentRef) == "" {
		return errors.New("audit: refund produced no durable adjustment")
	}
	if !refund.Idempotent && !refund.BalanceCredited {
		return billing.ErrRefundBalanceRowMissing
	}
	return nil
}

func classifyMismatchRefundHandlerError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, billing.ErrRefundNoCapturedCharge) ||
		errors.Is(err, billing.ErrRefundBalanceRowMissing) ||
		errors.Is(err, billing.ErrRefundAmountNotCovered) ||
		errors.Is(err, billing.ErrRefundIdempotencyConflict) ||
		errors.Is(err, billing.ErrRefundFactInvalid) ||
		errors.Is(err, ErrReceiptInvalidDerivedData) ||
		strings.Contains(err.Error(), "refund produced no durable adjustment") ||
		strings.Contains(err.Error(), "satisfied refund missing prior adjustment evidence") {
		return fmt.Errorf("%w: %w", dlq.ErrUnretryable, err)
	}
	return err
}

func mismatchRefundIdempotencyKey(claimID int64) string {
	return fmt.Sprintf("audit_mismatch_refund:%d", claimID)
}

func refundAuditRequestID(requestID string, claimID int64) string {
	return fmt.Sprintf("%s#audit_refund_%d", strings.TrimSpace(requestID), claimID)
}
