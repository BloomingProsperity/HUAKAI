package observability

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	runtimeconfig "github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
)

var ErrAuditRefMissing = errors.New("observability: audit ledger reference missing")

type AuditLoggerHandler struct {
	timeout        time.Duration
	requireRef     bool
	auditRefPolicy *eventbus.AuditRefPolicy
	observations   *AuditObservationStore
}

type AuditLoggerOption func(*AuditLoggerHandler)

func WithRequiredAuditRef() AuditLoggerOption {
	return func(h *AuditLoggerHandler) { h.requireRef = true }
}

func WithAuditRefPolicy(policy *eventbus.AuditRefPolicy) AuditLoggerOption {
	return func(h *AuditLoggerHandler) { h.auditRefPolicy = policy }
}

func WithAuditObservationStore(store *AuditObservationStore) AuditLoggerOption {
	return func(h *AuditLoggerHandler) { h.observations = store }
}

func NewAuditLoggerHandler(timeout time.Duration, opts ...AuditLoggerOption) *AuditLoggerHandler {
	h := &AuditLoggerHandler{timeout: timeout}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

func (h *AuditLoggerHandler) ID() eventbus.HandlerID {
	return eventbus.HandlerAuditLogger
}

func (h *AuditLoggerHandler) Tier() eventbus.Tier {
	return eventbus.TierHigh
}

func (h *AuditLoggerHandler) Order() int {
	return 20
}

func (h *AuditLoggerHandler) Critical() bool {
	return true
}

func (h *AuditLoggerHandler) Timeout() time.Duration {
	return h.timeout
}

func (h *AuditLoggerHandler) DLQKind() dlq.EventKind {
	return dlq.EventKindAuditEventReplica
}

func (h *AuditLoggerHandler) Handle(ctx context.Context, event eventbus.RequestCompletionEvent) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if h != nil && h.requireRef && event.AuditLedgerID == "" && event.AuditLedgerDLQRef == "" {
		if h.auditRefPolicy != nil && h.auditRefPolicy.AllowMissingMoneyRef {
			// 逃生开关只消除 audit logger 的二次拒绝，必须保留 ERROR 可见性。
			slog.Default().LogAttrs(ctx, slog.LevelError, "audit logger missing money audit ref allowed by escape flag",
				slog.String("request_id", event.RequestID),
				slog.Int64("tenant_id", event.TenantID),
				slog.String("route_id", auditLoggerRouteID(event)),
				slog.String("env_var", runtimeconfig.EnvTrustLedgerAllowMissingMoneyRef),
				slog.String("source", "audit_logger_escape_bypass"),
			)
		} else {
			return ErrAuditRefMissing
		}
	}
	if h != nil && h.observations != nil {
		h.observations.Append(AuditObservation{
			EventID:     event.ID,
			RequestID:   event.RequestID,
			TenantID:    event.TenantID,
			ClaimID:     event.ClaimID,
			LedgerID:    event.AuditLedgerID,
			Fingerprint: event.AuditSignatureFingerprint,
			ObservedAt:  time.Now().UTC(),
		})
	}
	return nil
}

func auditLoggerRouteID(event eventbus.RequestCompletionEvent) string {
	if event.Metadata == nil {
		return ""
	}
	return event.Metadata["route_id"]
}

type AuditObservation struct {
	EventID     string
	RequestID   string
	TenantID    int64
	ClaimID     int64
	LedgerID    string
	Fingerprint string
	ObservedAt  time.Time
}

type AuditObservationStore struct {
	mu    sync.Mutex
	items []AuditObservation
}

func (s *AuditObservationStore) Append(obs AuditObservation) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append(s.items, obs)
}

func (s *AuditObservationStore) Snapshot() []AuditObservation {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AuditObservation, len(s.items))
	copy(out, s.items)
	return out
}
