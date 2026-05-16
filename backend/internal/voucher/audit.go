package voucher

import (
	"context"
	"strings"
	"sync"
	"time"
)

const (
	AuditVoucherCreated          = "voucher_created"
	AuditVoucherRedeemed         = "voucher_redeemed"
	AuditVoucherRevoked          = "voucher_revoked"
	AuditVoucherExpired          = "voucher_expired"
	AuditVoucherRedeemFailed     = "voucher_redeem_failed"
	AuditVoucherRedeemBurstAlert = "voucher_redeem_burst_alert"
)

type AuditEvent struct {
	EventType       string         `json:"event_type"`
	TenantID        int64          `json:"tenant_id"`
	VoucherID       int64          `json:"voucher_id,omitempty"`
	BatchID         int64          `json:"batch_id,omitempty"`
	RedemptionID    int64          `json:"redemption_id,omitempty"`
	UserID          int64          `json:"user_id,omitempty"`
	ActorID         string         `json:"actor_id,omitempty"`
	RequestID       string         `json:"request_id,omitempty"`
	ReasonClass     string         `json:"reason_class,omitempty"`
	CodeFingerprint string         `json:"code_fingerprint,omitempty"`
	Payload         map[string]any `json:"payload,omitempty"`
	OccurredAt      time.Time      `json:"occurred_at"`
}

type AuditSink interface {
	EmitVoucherAudit(context.Context, AuditEvent) error
}

type NoopAuditSink struct{}

func (NoopAuditSink) EmitVoucherAudit(context.Context, AuditEvent) error { return nil }

type MemoryAuditSink struct {
	mu     sync.Mutex
	events []AuditEvent
}

func NewMemoryAuditSink() *MemoryAuditSink {
	return &MemoryAuditSink{}
}

func (s *MemoryAuditSink) EmitVoucherAudit(_ context.Context, event AuditEvent) error {
	if err := ValidateAuditEvent(event); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, cloneAuditEvent(event))
	return nil
}

func (s *MemoryAuditSink) Events() []AuditEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AuditEvent, len(s.events))
	for i := range s.events {
		out[i] = cloneAuditEvent(s.events[i])
	}
	return out
}

func ValidateAuditEvent(event AuditEvent) error {
	for k := range event.Payload {
		lower := strings.ToLower(k)
		if lower == "code" || strings.Contains(lower, "raw_code") || strings.Contains(lower, "voucher_code") {
			return ErrAuditCodeLeakBlocked
		}
	}
	return nil
}

func cloneAuditEvent(event AuditEvent) AuditEvent {
	if event.Payload != nil {
		payload := make(map[string]any, len(event.Payload))
		for k, v := range event.Payload {
			payload[k] = v
		}
		event.Payload = payload
	}
	return event
}
