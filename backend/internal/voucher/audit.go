package voucher

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
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

// PrivacyLogAuditSink 把 voucher 审计事件发到 privacy.system 结构化日志。
// 生产 wiring 此前没传 sink → 默认 NoopAuditSink,grant/redeem/revoke 审计被
// 静默丢弃(审计抓到的 dormant)。复用 ValidateAuditEvent 防泄漏门(CMB-5:
// payload 绝不带 code 明文,违规直接拒发)。
type PrivacyLogAuditSink struct{}

func (PrivacyLogAuditSink) EmitVoucherAudit(ctx context.Context, event AuditEvent) error {
	event.Payload = sanitizeAuditPayload(ctx, event.Payload)
	if err := ValidateAuditEvent(event); err != nil {
		return err
	}
	attrs := map[string]any{
		"event_class":      event.EventType,
		"tenant_id":        event.TenantID,
		"voucher_id":       event.VoucherID,
		"batch_id":         event.BatchID,
		"redemption_id":    event.RedemptionID,
		"user_id":          event.UserID,
		"actor_id":         event.ActorID,
		"reason_class":     event.ReasonClass,
		"code_fingerprint": event.CodeFingerprint,
		"occurred_at":      event.OccurredAt.UTC().Format(time.RFC3339),
	}
	for k, v := range event.Payload {
		attrs["payload_"+k] = v
	}
	return privacy.LogSystem(ctx, privacy.SystemEvent{
		Severity:  privacy.SeverityInfo,
		Component: "voucher.audit",
		RequestID: event.RequestID,
		Attrs:     attrs,
	})
}

type MemoryAuditSink struct {
	mu     sync.Mutex
	events []AuditEvent
}

func NewMemoryAuditSink() *MemoryAuditSink {
	return &MemoryAuditSink{}
}

func (s *MemoryAuditSink) EmitVoucherAudit(ctx context.Context, event AuditEvent) error {
	event.Payload = sanitizeAuditPayload(ctx, event.Payload)
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
	if event.Payload != nil {
		raw, _ := json.Marshal(event.Payload)
		if ContainsForbiddenVoucherAuditData(raw) {
			return ErrAuditCodeLeakBlocked
		}
	}
	for k := range event.Payload {
		lower := strings.ToLower(k)
		if lower == "code" || strings.Contains(lower, "raw_code") || strings.Contains(lower, "voucher_code") {
			return ErrAuditCodeLeakBlocked
		}
	}
	return nil
}

func ContainsForbiddenVoucherAuditData(raw []byte) bool {
	return privacy.ContainsForbiddenRawData(raw)
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
