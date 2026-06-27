package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
    claim_id, request_id, delta_micro_usd, status, created_at, tenant_id
) VALUES (
    $1, $2, $3, 'pending', COALESCE($4::timestamptz, now()), $5
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
		payload.TenantID,
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
	AppendInTx(context.Context, pgx.Tx, auditledger.PreparedEntry) (auditledger.LedgerEntry, error)
}

type refundReceiptSinkInTx interface {
	AppendRefundReceiptInTx(context.Context, pgx.Tx, *CostReceipt) error
}

type refundReceiptSequenceReader interface {
	GetReceiptBySequence(context.Context, string, int64, int32) (*CostReceipt, error)
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

// QuotaReverser 把已结算 claim 的成本从配额 settled_value 负向冲减(退款后的次级补账)。
// 退款只退钱包、不退配额计数会让用户净花费已降却因旧计数提前撞成本上限被拒(过度强制),
// 本接口在退款落库后按退款的实际金额把 settled_value 同步降回。实现由 wiring 注入(quota.Service
// 适配器);未注入则退款不触发配额冲减,行为与改造前完全一致(零回归)。
type QuotaReverser interface {
	ReverseSettledCost(ctx context.Context, tenantID, claimID int64, amountMicroUSD int64) error
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

// WithRefundQuotaReverser 注入配额成本冲减器:退款落库后,把退款的实际金额从配额 settled_value
// 负向冲减(post-commit 次级补账,fail-open)。不注入则退款不触发配额冲减(零行为变化)。
func WithRefundQuotaReverser(reverser QuotaReverser) RefundWorkerOption {
	return RefundWorkerOption{applyWorker: func(w *MismatchRefundWorker) {
		if reverser != nil {
			w.quotaReverser = reverser
		}
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
	if completed, err := w.completeExistingRefundReceipt(ctx, payload); err != nil {
		_ = w.pending.MarkFailed(ctx, payload.ClaimID)
		return err
	} else if completed {
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
	w.reverseQuotaAfterRefund(ctx, payload, refund)
	return w.pending.MarkCompleted(ctx, payload.ClaimID, w.now())
}

// reverseQuotaAfterRefund 在 billing 退款落库后,把退款的实际金额从配额 settled_value 负向冲减,
// 修补"退款只退钱包、配额计数没退→用户提前撞成本上限"的过度强制。语义要点:
//   - post-commit 次级补账、fail-open:失败只记 WARN 日志,不影响已成功的退款(与 quotaenforce 的
//     Settle/CommitCacheHit post-commit 补账一致;quota 刻意不接管外部事务)。
//   - 冲减额=本轮退款的实际金额 RefundMicroUSD(已被 billing 按 claim 原计费额累计封顶),与钱包退的
//     同一 delta 对齐。
//   - 防双冲减:① DLQ 重投在 Apply 入口被 pending(completed)/ 已存退款回执短路,到不了这里;② 即便
//     到了,billing 对同 audit_request_id 的幂等重放返回的是【存储的正退款额 + Idempotent=true】(不是
//     0,RefundMicroUSD<=0 守卫对此无效),故这里显式判 refund.Idempotent 跳过——本轮非新退款、settled_value
//     已在原始调用时冲过,绝不二次冲减。RefundMicroUSD<=0 守卫只覆盖"本就无退款"(amount=0 / 累计封顶到
//     remaining=0)这一类,不承担重放去重职责。
func (w *MismatchRefundWorker) reverseQuotaAfterRefund(ctx context.Context, payload MismatchRefundPayload, refund *billing.RefundResult) {
	if w == nil || w.quotaReverser == nil || refund == nil || refund.RefundMicroUSD <= 0 || refund.Idempotent {
		return
	}
	if err := w.quotaReverser.ReverseSettledCost(ctx, payload.TenantID, payload.ClaimID, refund.RefundMicroUSD); err != nil {
		slog.WarnContext(ctx, "退款后配额 settled_value 冲减失败(fail-open,不影响已成功退款)",
			slog.Int64("tenant_id", payload.TenantID),
			slog.Int64("claim_id", payload.ClaimID),
			slog.Int64("refund_micro_usd", refund.RefundMicroUSD),
			slog.String("error", err.Error()))
	}
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
	w.reverseQuotaAfterRefund(ctx, payload, refund)
	return w.pending.MarkCompleted(ctx, payload.ClaimID, w.now())
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
	return refundLedgerRef(requestID, entry), nil
}

func (w *MismatchRefundWorker) signRefundedReceipt(ctx context.Context, payload MismatchRefundPayload, refs []string) error {
	if w == nil || w.formatter == nil {
		return nil
	}
	if _, err := w.existingRefundReceiptByIdempotency(ctx, payload); err == nil {
		return nil
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
				if _, getErr := w.existingRefundReceiptByIdempotency(ctx, payload); getErr != nil {
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
	if _, err := w.existingRefundReceiptByIdempotency(ctx, payload); err == nil {
		return nil
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
		if _, err := w.existingRefundReceiptByIdempotency(ctx, payload); err == nil {
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

func (w *MismatchRefundWorker) completeExistingRefundReceipt(ctx context.Context, payload MismatchRefundPayload) (bool, error) {
	if w == nil || w.pending == nil {
		return false, nil
	}
	if _, err := w.existingRefundReceiptByIdempotency(ctx, payload); err == nil {
		if markErr := w.pending.MarkCompleted(ctx, payload.ClaimID, w.now()); markErr != nil {
			return false, markErr
		}
		return true, nil
	} else {
		if !errors.Is(err, ErrReceiptNotFound) && !errors.Is(err, ErrReceiptStorageRequired) {
			return false, fmt.Errorf("audit: lookup existing refunded receipt: %w", err)
		}
	}
	return false, nil
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

func (w *MismatchRefundWorker) existingRefundReceipt(ctx context.Context, payload MismatchRefundPayload, sequence int32) (*CostReceipt, error) {
	if w == nil || w.receiptSink == nil {
		return nil, ErrReceiptStorageRequired
	}
	if reader, ok := w.receiptSink.(refundReceiptSequenceReader); ok {
		receipt, err := reader.GetReceiptBySequence(ctx, payload.RequestID, payload.TenantID, sequence)
		if err != nil {
			return nil, err
		}
		return validateExistingRefundReceipt(receipt, payload, sequence)
	}
	if reader, ok := w.receiptSink.(refundReceiptLatestReader); ok {
		receipt, err := reader.GetReceiptForAdmin(ctx, payload.RequestID, payload.TenantID)
		if err != nil {
			return nil, err
		}
		if receipt == nil || receipt.ReceiptSequence != sequence {
			return nil, ErrReceiptNotFound
		}
		return validateExistingRefundReceipt(receipt, payload, sequence)
	}
	return nil, ErrReceiptStorageRequired
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

func validateExistingRefundReceiptByIdempotency(receipt *CostReceipt, payload MismatchRefundPayload, key string) (*CostReceipt, error) {
	if receipt == nil {
		return nil, ErrReceiptNotFound
	}
	if strings.TrimSpace(receipt.RequestID) != strings.TrimSpace(payload.RequestID) ||
		receipt.TenantID != payload.TenantID {
		return nil, fmt.Errorf("%w: refunded receipt idempotency mismatch", ErrReceiptInvalidDerivedData)
	}
	if NormalizeReceiptValidationState(receipt.ValidationState) != ReceiptValidationStateMismatchRefunded {
		return nil, fmt.Errorf("%w: refunded receipt state mismatch", ErrReceiptInvalidDerivedData)
	}
	if !adjustmentRefsContain(receipt.AdjustmentRefs, key) {
		return nil, fmt.Errorf("%w: refunded receipt idempotency ref mismatch", ErrReceiptInvalidDerivedData)
	}
	return receipt, nil
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
