package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
)

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

type MismatchRefundEligibilityVerifier interface {
	VerifyRefundableCharge(context.Context, billing.RefundRequest) error
}

type MismatchRefundQueue struct {
	service     MismatchRefundEnqueueService
	eligibility MismatchRefundEligibilityVerifier
	now         func() time.Time
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
	if receipt == nil {
		return 0, ErrReceiptRequired
	}
	if !verdict.RefundEligible() {
		return 0, fmt.Errorf("%w: mismatch verdict is not refund eligible", ErrReceiptInvalidDerivedData)
	}
	if q.eligibility == nil {
		return 0, errors.New("audit: mismatch refund eligibility verifier required")
	}
	if err := q.eligibility.VerifyRefundableCharge(ctx, billing.RefundRequest{
		TenantID:       receipt.TenantID,
		ClaimID:        receipt.ClaimID,
		AmountMicroUSD: verdict.DeltaMicroUSD,
	}); err != nil {
		return 0, err
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
	if !verdict.RefundEligible() {
		return dlq.Event{}, fmt.Errorf("%w: mismatch verdict is not refund eligible", ErrReceiptInvalidDerivedData)
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
