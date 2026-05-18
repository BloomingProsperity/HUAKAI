package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

const AuditMismatchRefundReason = "audit_mismatch_v1"

type MismatchRefundPayload struct {
	TenantID       int64    `json:"tenant_id"`
	ClaimID        int64    `json:"claim_id"`
	RequestID      string   `json:"request_id"`
	DeltaMicroUSD  int64    `json:"delta_micro_usd"`
	FieldsMismatch []string `json:"fields_mismatch"`
	CreatedAt      string   `json:"created_at"`
}

type MismatchRefundEnqueueService interface {
	Enqueue(context.Context, dlq.Event) (int64, error)
}

type MismatchRefundQueue struct {
	service MismatchRefundEnqueueService
	now     func() time.Time
}

func NewMismatchRefundQueue(service MismatchRefundEnqueueService, opts ...RefundWorkerOption) *MismatchRefundQueue {
	q := &MismatchRefundQueue{service: service, now: func() time.Time { return time.Now().UTC() }}
	for _, opt := range opts {
		if opt.applyQueue != nil {
			opt.applyQueue(q)
		}
	}
	return q
}

func (q *MismatchRefundQueue) EnqueueMismatchRefund(ctx context.Context, receipt *CostReceipt, verdict MismatchVerdict) (int64, error) {
	if q == nil || q.service == nil {
		return 0, dlq.ErrStoreNotConfigured
	}
	event, err := NewMismatchRefundEvent(receipt, verdict, q.now())
	if err != nil {
		return 0, err
	}
	return q.service.Enqueue(ctx, event)
}

func NewMismatchRefundEvent(receipt *CostReceipt, verdict MismatchVerdict, now time.Time) (dlq.Event, error) {
	if receipt == nil {
		return dlq.Event{}, ErrReceiptRequired
	}
	if receipt.TenantID <= 0 || receipt.ClaimID <= 0 {
		return dlq.Event{}, fmt.Errorf("%w: refund tenant_id/claim_id missing", ErrReceiptInvalidDerivedData)
	}
	if err := validateReceiptRequestID(receipt.RequestID); err != nil {
		return dlq.Event{}, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	payload := MismatchRefundPayload{
		TenantID:       receipt.TenantID,
		ClaimID:        receipt.ClaimID,
		RequestID:      receipt.RequestID,
		DeltaMicroUSD:  verdict.DeltaMicroUSD,
		FieldsMismatch: append([]string(nil), verdict.FieldsMismatch...),
		CreatedAt:      now.UTC().Format(time.RFC3339Nano),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return dlq.Event{}, err
	}
	return dlq.Event{
		TenantID:       receipt.TenantID,
		ClaimID:        receipt.ClaimID,
		EventKind:      dlq.EventKindAuditMismatchRefund,
		Lane:           dlq.LaneHigh,
		Payload:        raw,
		FailureReason:  AuditMismatchRefundReason,
		IdempotencyKey: mismatchRefundIdempotencyKey(receipt.ClaimID),
		SourceTable:    "audit_refund_pending",
		SourceID:       receipt.ClaimID,
		NextRetryAt:    now.UTC(),
	}, nil
}

type RefundPendingRecord struct {
	ClaimID       int64
	RequestID     string
	DeltaMicroUSD int64
	Status        string
}

type RefundPendingStore interface {
	EnsurePending(context.Context, MismatchRefundPayload) (RefundPendingRecord, error)
	MarkCompleted(context.Context, int64, time.Time) error
	MarkFailed(context.Context, int64) error
}

type PGXRefundPendingStore struct {
	pool *pgxpool.Pool
}

func NewPGXRefundPendingStore(pool *pgxpool.Pool) (*PGXRefundPendingStore, error) {
	if pool == nil {
		return nil, errors.New("audit: pgx pool required")
	}
	return &PGXRefundPendingStore{pool: pool}, nil
}

func (s *PGXRefundPendingStore) EnsurePending(ctx context.Context, payload MismatchRefundPayload) (RefundPendingRecord, error) {
	if s == nil || s.pool == nil {
		return RefundPendingRecord{}, ErrReceiptStorageRequired
	}
	if err := validateRefundPayload(payload); err != nil {
		return RefundPendingRecord{}, err
	}
	var rec RefundPendingRecord
	err := s.pool.QueryRow(ctx, `
INSERT INTO audit_refund_pending (
    claim_id, request_id, delta_micro_usd, status, created_at
) VALUES (
    $1, $2, $3, 'pending', COALESCE($4::timestamptz, now())
)
ON CONFLICT (claim_id) DO UPDATE SET
    status = CASE
        WHEN audit_refund_pending.status = 'completed' THEN 'completed'
        ELSE 'pending'
    END,
    completed_at = CASE
        WHEN audit_refund_pending.status = 'completed' THEN audit_refund_pending.completed_at
        ELSE NULL
    END
RETURNING claim_id, request_id, delta_micro_usd, status`,
		payload.ClaimID,
		payload.RequestID,
		payload.DeltaMicroUSD,
		nullablePayloadTime(payload.CreatedAt),
	).Scan(&rec.ClaimID, &rec.RequestID, &rec.DeltaMicroUSD, &rec.Status)
	if err != nil {
		return RefundPendingRecord{}, fmt.Errorf("audit: ensure refund pending: %w", err)
	}
	return rec, nil
}

func (s *PGXRefundPendingStore) MarkCompleted(ctx context.Context, claimID int64, completedAt time.Time) error {
	if s == nil || s.pool == nil {
		return ErrReceiptStorageRequired
	}
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx, `
UPDATE audit_refund_pending
SET status = 'completed', completed_at = $2
WHERE claim_id = $1`, claimID, completedAt.UTC())
	if err != nil {
		return fmt.Errorf("audit: mark refund completed: %w", err)
	}
	return nil
}

func (s *PGXRefundPendingStore) MarkFailed(ctx context.Context, claimID int64) error {
	if s == nil || s.pool == nil {
		return ErrReceiptStorageRequired
	}
	_, err := s.pool.Exec(ctx, `
UPDATE audit_refund_pending
SET status = 'failed', completed_at = NULL
WHERE claim_id = $1 AND status <> 'completed'`, claimID)
	if err != nil {
		return fmt.Errorf("audit: mark refund failed: %w", err)
	}
	return nil
}

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
	AppendInTx(context.Context, pgx.Tx, auditledger.LedgerEntry) (auditledger.LedgerEntry, error)
}

type refundReceiptSinkInTx interface {
	AppendRefundReceiptInTx(context.Context, pgx.Tx, *CostReceipt) error
}

type refundReceiptSequenceReader interface {
	GetReceiptBySequence(context.Context, string, int64, int32) (*CostReceipt, error)
}

type refundReceiptLatestReader interface {
	GetReceipt(context.Context, string, int64) (*CostReceipt, error)
}

type MismatchRefundWorker struct {
	pending     RefundPendingStore
	settler     billing.Settler
	formatter   receiptFormatterService
	ledger      auditledger.Ledger
	receiptSink RefundReceiptSink
	txBeginner  refundTransactionBeginner
	now         func() time.Time
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
	var payload MismatchRefundPayload
	if err := json.Unmarshal(rec.Payload, &payload); err != nil {
		return fmt.Errorf("audit: decode mismatch refund payload: %w", err)
	}
	if payload.TenantID == 0 {
		payload.TenantID = rec.TenantID
	}
	if payload.ClaimID == 0 && rec.ClaimID != nil {
		payload.ClaimID = *rec.ClaimID
	}
	return w.Apply(ctx, payload)
}

func (w *MismatchRefundWorker) Apply(ctx context.Context, payload MismatchRefundPayload) error {
	if err := validateRefundPayload(payload); err != nil {
		return err
	}
	rec, err := w.pending.EnsurePending(ctx, payload)
	if err != nil {
		return err
	}
	if rec.Status == "completed" {
		return nil
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
		AuditRequestID: refundAuditRequestID(payload.RequestID, payload.ClaimID),
	})
	if err != nil {
		_ = w.pending.MarkFailed(ctx, payload.ClaimID)
		return err
	}

	refs := []string{}
	if refund != nil && strings.TrimSpace(refund.AdjustmentRef) != "" {
		refs = append(refs, strings.TrimSpace(refund.AdjustmentRef))
	}
	if ledgerRef, err := w.appendRefundLedger(ctx, payload); err != nil {
		_ = w.pending.MarkFailed(ctx, payload.ClaimID)
		return err
	} else if ledgerRef != "" {
		refs = append(refs, ledgerRef)
	}
	if err := w.signRefundedReceipt(ctx, payload, refs); err != nil {
		_ = w.pending.MarkFailed(ctx, payload.ClaimID)
		return err
	}
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

	tx, err := w.txBeginner.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		_ = w.pending.MarkFailed(ctx, payload.ClaimID)
		return fmt.Errorf("audit: begin refund transaction: %w", err)
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
		AuditRequestID: refundAuditRequestID(payload.RequestID, payload.ClaimID),
	})
	if err != nil {
		_ = w.pending.MarkFailed(ctx, payload.ClaimID)
		return err
	}
	if refund != nil && strings.TrimSpace(refund.AdjustmentRef) != "" {
		refs = append(refs, strings.TrimSpace(refund.AdjustmentRef))
	}
	if ledgerRef, err := w.appendRefundLedgerInTx(ctx, tx, ledger, payload); err != nil {
		_ = w.pending.MarkFailed(ctx, payload.ClaimID)
		return err
	} else if ledgerRef != "" {
		refs = append(refs, ledgerRef)
	}
	if err := w.signRefundedReceiptInTx(ctx, tx, receiptSink, payload, refs); err != nil {
		_ = w.pending.MarkFailed(ctx, payload.ClaimID)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		_ = w.pending.MarkFailed(ctx, payload.ClaimID)
		return fmt.Errorf("audit: commit refund transaction: %w", err)
	}
	committed = true
	return w.pending.MarkCompleted(ctx, payload.ClaimID, w.now())
}

func (w *MismatchRefundWorker) appendRefundLedger(ctx context.Context, payload MismatchRefundPayload) (string, error) {
	if w == nil || w.ledger == nil {
		return "", nil
	}
	requestID := refundAuditRequestID(payload.RequestID, payload.ClaimID)
	entry, err := w.ledger.Append(ctx, auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  payload.TenantID,
		ModelChain: &proto.ModelChain{
			Requested:        "audit_mismatch_refund",
			RouteDecided:     "audit_mismatch_refund",
			UpstreamReported: "audit_mismatch_refund",
			Verdict:          "mismatch",
		},
	})
	if isRefundLedgerDuplicate(err) {
		existing, getErr := w.ledger.GetByRequestID(ctx, requestID)
		if getErr != nil {
			return "", fmt.Errorf("audit: lookup duplicate refund ledger entry: %w", getErr)
		}
		return refundLedgerRef(requestID, existing), nil
	}
	if err != nil {
		return "", fmt.Errorf("audit: append refund ledger entry: %w", err)
	}
	return refundLedgerRef(requestID, entry), nil
}

func (w *MismatchRefundWorker) appendRefundLedgerInTx(ctx context.Context, tx pgx.Tx, ledger refundLedgerInTx, payload MismatchRefundPayload) (string, error) {
	if w == nil || ledger == nil {
		return "", nil
	}
	requestID := refundAuditRequestID(payload.RequestID, payload.ClaimID)
	if existing, err := w.ledger.GetByRequestID(ctx, requestID); err == nil {
		return refundLedgerRef(requestID, existing), nil
	} else if !errors.Is(err, auditledger.ErrLedgerEntryNotFound) {
		return "", fmt.Errorf("audit: lookup existing refund ledger entry: %w", err)
	}
	entry, err := ledger.AppendInTx(ctx, tx, auditledger.LedgerEntry{
		RequestID: requestID,
		TenantID:  payload.TenantID,
		ModelChain: &proto.ModelChain{
			Requested:        "audit_mismatch_refund",
			RouteDecided:     "audit_mismatch_refund",
			UpstreamReported: "audit_mismatch_refund",
			Verdict:          "mismatch",
		},
	})
	if err != nil {
		return "", fmt.Errorf("audit: append refund ledger entry: %w", err)
	}
	return refundLedgerRef(requestID, entry), nil
}

func (w *MismatchRefundWorker) signRefundedReceipt(ctx context.Context, payload MismatchRefundPayload, refs []string) error {
	if w == nil || w.formatter == nil {
		return nil
	}
	receipt, err := w.formatter.DeriveReceipt(ctx, payload.RequestID)
	if err != nil {
		return fmt.Errorf("audit: derive refunded receipt: %w", err)
	}
	receipt.ClaimID = payload.ClaimID
	receipt.ReceiptSequence = 1
	receipt.ValidationState = ReceiptValidationStateMismatchRefunded
	receipt.Verdict = ReceiptVerdictMismatchRefundPending
	receipt.AdjustmentRefs = refs
	signed, err := w.formatter.SignReceipt(ctx, receipt)
	if err != nil {
		return fmt.Errorf("audit: sign refunded receipt: %w", err)
	}
	if w.receiptSink != nil {
		if err := w.receiptSink.AppendRefundReceipt(ctx, signed); err != nil {
			if isRefundReceiptDuplicate(err) {
				if _, getErr := w.existingRefundReceipt(ctx, payload); getErr != nil {
					return fmt.Errorf("audit: lookup duplicate refunded receipt: %w", getErr)
				}
				return nil
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
	receipt, err := w.formatter.DeriveReceipt(ctx, payload.RequestID)
	if err != nil {
		return fmt.Errorf("audit: derive refunded receipt: %w", err)
	}
	receipt.ClaimID = payload.ClaimID
	receipt.ReceiptSequence = 1
	receipt.ValidationState = ReceiptValidationStateMismatchRefunded
	receipt.Verdict = ReceiptVerdictMismatchRefundPending
	receipt.AdjustmentRefs = refs
	signed, err := w.formatter.SignReceipt(ctx, receipt)
	if err != nil {
		return fmt.Errorf("audit: sign refunded receipt: %w", err)
	}
	if sink != nil {
		if _, err := w.existingRefundReceipt(ctx, payload); err == nil {
			return nil
		} else if !errors.Is(err, ErrReceiptNotFound) && !errors.Is(err, ErrReceiptStorageRequired) {
			return fmt.Errorf("audit: lookup duplicate refunded receipt: %w", err)
		}
		if err := sink.AppendRefundReceiptInTx(ctx, tx, signed); err != nil {
			return fmt.Errorf("audit: append refunded receipt: %w", err)
		}
	}
	return nil
}

func (w *MismatchRefundWorker) existingRefundReceipt(ctx context.Context, payload MismatchRefundPayload) (*CostReceipt, error) {
	if w == nil || w.receiptSink == nil {
		return nil, ErrReceiptStorageRequired
	}
	const refundedSequence int32 = 1
	if reader, ok := w.receiptSink.(refundReceiptSequenceReader); ok {
		receipt, err := reader.GetReceiptBySequence(ctx, payload.RequestID, payload.TenantID, refundedSequence)
		if err != nil {
			return nil, err
		}
		return validateExistingRefundReceipt(receipt, payload, refundedSequence)
	}
	if reader, ok := w.receiptSink.(refundReceiptLatestReader); ok {
		receipt, err := reader.GetReceipt(ctx, payload.RequestID, payload.TenantID)
		if err != nil {
			return nil, err
		}
		return validateExistingRefundReceipt(receipt, payload, refundedSequence)
	}
	return nil, ErrReceiptStorageRequired
}

func validateExistingRefundReceipt(receipt *CostReceipt, payload MismatchRefundPayload, sequence int32) (*CostReceipt, error) {
	if receipt == nil {
		return nil, ErrReceiptNotFound
	}
	if strings.TrimSpace(receipt.RequestID) != strings.TrimSpace(payload.RequestID) ||
		receipt.TenantID != payload.TenantID ||
		receipt.ReceiptSequence != sequence {
		return nil, fmt.Errorf("%w: refunded receipt duplicate mismatch", ErrReceiptInvalidDerivedData)
	}
	if NormalizeReceiptValidationState(receipt.ValidationState) != ReceiptValidationStateMismatchRefunded {
		return nil, fmt.Errorf("%w: refunded receipt state mismatch", ErrReceiptInvalidDerivedData)
	}
	return receipt, nil
}

func refundLedgerRef(requestID string, entry auditledger.LedgerEntry) string {
	if strings.TrimSpace(entry.LedgerID) != "" {
		return "audit_ledger:" + strings.TrimSpace(entry.LedgerID)
	}
	return "audit_ledger:" + strings.TrimSpace(requestID)
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
	if payload.TenantID <= 0 || payload.ClaimID <= 0 || payload.DeltaMicroUSD < 0 {
		return fmt.Errorf("%w: refund payload", ErrReceiptInvalidDerivedData)
	}
	return validateReceiptRequestID(payload.RequestID)
}

func nullablePayloadTime(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	ts, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	return ts.UTC()
}

func mismatchRefundIdempotencyKey(claimID int64) string {
	return fmt.Sprintf("audit_mismatch_refund:%d", claimID)
}

func refundAuditRequestID(requestID string, claimID int64) string {
	return fmt.Sprintf("%s#audit_refund_%d", strings.TrimSpace(requestID), claimID)
}

type MemoryRefundPendingStore struct {
	mu   sync.Mutex
	rows map[int64]RefundPendingRecord
}

func NewMemoryRefundPendingStore() *MemoryRefundPendingStore {
	return &MemoryRefundPendingStore{rows: map[int64]RefundPendingRecord{}}
}

func (s *MemoryRefundPendingStore) EnsurePending(_ context.Context, payload MismatchRefundPayload) (RefundPendingRecord, error) {
	if err := validateRefundPayload(payload); err != nil {
		return RefundPendingRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rows == nil {
		s.rows = map[int64]RefundPendingRecord{}
	}
	rec, ok := s.rows[payload.ClaimID]
	if !ok {
		rec = RefundPendingRecord{
			ClaimID:       payload.ClaimID,
			RequestID:     payload.RequestID,
			DeltaMicroUSD: payload.DeltaMicroUSD,
			Status:        "pending",
		}
		s.rows[payload.ClaimID] = rec
		return rec, nil
	}
	if rec.Status != "completed" {
		rec.Status = "pending"
		s.rows[payload.ClaimID] = rec
	}
	return rec, nil
}

func (s *MemoryRefundPendingStore) MarkCompleted(_ context.Context, claimID int64, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.rows[claimID]
	rec.Status = "completed"
	s.rows[claimID] = rec
	return nil
}

func (s *MemoryRefundPendingStore) MarkFailed(_ context.Context, claimID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.rows[claimID]
	if rec.Status != "completed" {
		rec.Status = "failed"
		s.rows[claimID] = rec
	}
	return nil
}

func (s *MemoryRefundPendingStore) Status(claimID int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rows[claimID].Status
}
