package dlq

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type RefundPayload struct {
	TenantID         int64  `json:"tenant_id"`
	ReceiptID        string `json:"receipt_id"`
	RequestID        string `json:"request_id,omitempty"`
	ReasonClass      string `json:"reason_class"`
	RefundMicrocents int64  `json:"refund_microcents"`
	CreatedAt        string `json:"created_at"`
}

type RefundSink interface {
	ApplyMismatchRefund(context.Context, RefundPayload) error
}

func NewRefundEvent(payload RefundPayload) (OutboxEvent, error) {
	if payload.CreatedAt == "" {
		payload.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return OutboxEvent{}, err
	}
	return OutboxEvent{
		TenantID:  payload.TenantID,
		EventType: EventTypeAuditRefund,
		Priority:  PriorityHigh,
		Payload:   raw,
	}, nil
}

func NewRefundHandler(sink RefundSink) Handler {
	return func(ctx context.Context, ev OutboxEvent) error {
		if sink == nil {
			return ErrOutboxNotConfigured
		}
		var payload RefundPayload
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			return fmt.Errorf("obsdlq: decode refund payload: %w", err)
		}
		if payload.TenantID <= 0 {
			payload.TenantID = ev.TenantID
		}
		if payload.TenantID <= 0 || payload.ReceiptID == "" || payload.RefundMicrocents < 0 {
			return fmt.Errorf("%w: refund payload", ErrInvalidEvent)
		}
		return sink.ApplyMismatchRefund(ctx, payload)
	}
}
