package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
)

type EventKind string

const (
	EventKindRequestCompletion EventKind = "request_completion"
)

type HandlerID string

const (
	HandlerBillingPersister    HandlerID = "billing_persister"
	HandlerAuditLogger         HandlerID = "audit_logger"
	HandlerReconciliationCheck HandlerID = "reconciliation_check"
	HandlerAccountHealthProbe  HandlerID = "account_health_probe"
	HandlerMetricsAggregator   HandlerID = "metrics_aggregator"
)

type Tier string

const (
	TierHigh Tier = "HIGH"
	TierMed  Tier = "MED"
	TierLow  Tier = "LOW"
)

type HandlerState string

const (
	HandlerStateInflight HandlerState = "inflight"
	HandlerStateDone     HandlerState = "done"
	HandlerStateFailed   HandlerState = "failed"
)

var (
	ErrNoHandlers      = errors.New("eventbus: no handlers registered")
	ErrBusClosed       = errors.New("eventbus: bus closed")
	ErrQueueFull       = errors.New("eventbus: handler queue full")
	ErrHandlerTimeout  = errors.New("eventbus: handler timeout")
	ErrHandlerPanic    = errors.New("eventbus: handler panic")
	ErrHandlerDropped  = errors.New("eventbus: async handler dropped")
	ErrInvalidHandler  = errors.New("eventbus: invalid handler")
	ErrInvalidEvent    = errors.New("eventbus: invalid event")
	ErrCriticalHandler = errors.New("eventbus: critical handler failed")
)

type RequestCompletionEvent struct {
	ID                        string
	Kind                      EventKind
	TenantID                  int64
	ClaimID                   int64
	AccountID                 int64
	RequestID                 string
	EndpointFamily            string
	RequestedModel            string
	UpstreamModel             string
	PayloadHash               string
	RawBodyHash               string
	RedactedBodyRef           string
	AuditLedgerID             string
	AuditLedgerDLQRef         string
	AuditSignatureFingerprint string
	CreatedAt                 time.Time
	SettleRequest             billing.SettleRequest
	Metadata                  map[string]string
}

type Handler interface {
	ID() HandlerID
	Tier() Tier
	Order() int
	Critical() bool
	Timeout() time.Duration
	DLQKind() dlq.EventKind
	Handle(context.Context, RequestCompletionEvent) error
}

type HandlerFunc struct {
	HandlerID      HandlerID
	HandlerTier    Tier
	HandlerOrder   int
	IsCritical     bool
	HandlerTimeout time.Duration
	HandlerDLQKind dlq.EventKind
	Fn             func(context.Context, RequestCompletionEvent) error
}

func (h HandlerFunc) ID() HandlerID {
	return h.HandlerID
}

func (h HandlerFunc) Tier() Tier {
	if h.HandlerTier == "" {
		return TierMed
	}
	return h.HandlerTier
}

func (h HandlerFunc) Order() int {
	return h.HandlerOrder
}

func (h HandlerFunc) Critical() bool {
	return h.IsCritical
}

func (h HandlerFunc) Timeout() time.Duration {
	return h.HandlerTimeout
}

func (h HandlerFunc) DLQKind() dlq.EventKind {
	return h.HandlerDLQKind
}

func (h HandlerFunc) Handle(ctx context.Context, event RequestCompletionEvent) error {
	if h.Fn == nil {
		return nil
	}
	return h.Fn(ctx, event)
}

type Config struct {
	Enabled              bool
	HighWorkers          int
	MediumWorkers        int
	LowWorkers           int
	HighBuffer           int
	MediumBuffer         int
	LowBuffer            int
	HandlerTimeout       time.Duration
	ShutdownDrainTimeout time.Duration
	AuditRefPolicy       *AuditRefPolicy
	// MaxStates 限制每个 handler 状态账本的大小。正值会给该 map 设置上限,
	// 溢出时淘汰最旧的条目;值 <= 0 时账本不设上限(即历史上的、作为应急
	// 出口的行为)。NormalizeConfig 刻意不对该字段做归一处理,以便 0 始终
	// 表示不设上限。
	MaxStates int
}

type DropNotice struct {
	HandlerID HandlerID
	Tier      Tier
	EventID   string
	Reason    string
	DroppedAt time.Time
}

type DLQSink interface {
	Enqueue(context.Context, dlq.Event) (int64, error)
}

type StateKey struct {
	EventID   string
	HandlerID HandlerID
}

type StateSnapshot struct {
	EventID   string
	HandlerID HandlerID
	State     HandlerState
	Error     string
	UpdatedAt time.Time
}

func DefaultConfig() Config {
	return Config{
		Enabled:              true,
		HighWorkers:          2,
		MediumWorkers:        1,
		LowWorkers:           1,
		HighBuffer:           128,
		MediumBuffer:         256,
		LowBuffer:            512,
		HandlerTimeout:       3 * time.Second,
		ShutdownDrainTimeout: 5 * time.Second,
	}
}

func NormalizeConfig(cfg Config) Config {
	def := DefaultConfig()
	if cfg.HighWorkers <= 0 {
		cfg.HighWorkers = def.HighWorkers
	}
	if cfg.MediumWorkers <= 0 {
		cfg.MediumWorkers = def.MediumWorkers
	}
	if cfg.LowWorkers <= 0 {
		cfg.LowWorkers = def.LowWorkers
	}
	if cfg.HighBuffer <= 0 {
		cfg.HighBuffer = def.HighBuffer
	}
	if cfg.MediumBuffer <= 0 {
		cfg.MediumBuffer = def.MediumBuffer
	}
	if cfg.LowBuffer <= 0 {
		cfg.LowBuffer = def.LowBuffer
	}
	if cfg.HandlerTimeout <= 0 {
		cfg.HandlerTimeout = def.HandlerTimeout
	}
	if cfg.ShutdownDrainTimeout <= 0 {
		cfg.ShutdownDrainTimeout = def.ShutdownDrainTimeout
	}
	return cfg
}

func (e RequestCompletionEvent) normalized(policy *AuditRefPolicy) (RequestCompletionEvent, error) {
	if e.Kind == "" {
		e.Kind = EventKindRequestCompletion
	}
	if e.Kind != EventKindRequestCompletion {
		return e, fmt.Errorf("%w: kind=%s", ErrInvalidEvent, e.Kind)
	}
	if e.ID == "" {
		if e.RequestID != "" {
			e.ID = e.RequestID
		} else if e.ClaimID > 0 {
			e.ID = fmt.Sprintf("claim:%d", e.ClaimID)
		}
	}
	if e.ID == "" || e.TenantID <= 0 {
		return e, ErrInvalidEvent
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	if e.Kind == EventKindRequestCompletion && policy != nil {
		if err := ValidateMoneyPathAuditRef(&e, policy); err != nil {
			return e, err
		}
	}
	return e, nil
}

// CustomDLQPayloadProvider 是可选 Handler 扩展接口。
//
// 当 handler 失败需要把 RequestCompletionEvent 转 DLQ 行时,默认 dlqPayload
// 生成的是 generic event 元数据(handler_id / failure_reason 等),适合
// observability 但不能给 worker 拿来重放业务逻辑。
//
// money-path handler(比如 BillingPersisterHandler)需要 DLQ 行带可重放
// payload(settlementrecovery.Payload),实现本接口返自定义 bytes。返 err
// 或 nil 时 dlqPayload 回退默认实现,保 observability 不丢。
type CustomDLQPayloadProvider interface {
	DLQPayload(event RequestCompletionEvent, handlerErr error) ([]byte, error)
}

func dlqPayload(event RequestCompletionEvent, h Handler, err error) json.RawMessage {
	if provider, ok := h.(CustomDLQPayloadProvider); ok {
		if raw, perr := provider.DLQPayload(event, err); perr == nil && len(raw) > 0 {
			return json.RawMessage(raw)
		}
		// 失败 fall through 到 default,保 DLQ 行至少有 observability 元数据。
	}
	payload := map[string]any{
		"event_id":       event.ID,
		"event_kind":     event.Kind,
		"handler_id":     h.ID(),
		"tenant_id":      event.TenantID,
		"claim_id":       event.ClaimID,
		"request_id":     event.RequestID,
		"payload_hash":   event.PayloadHash,
		"raw_body_hash":  event.RawBodyHash,
		"redacted_ref":   event.RedactedBodyRef,
		"failure_reason": classifyHandlerFailure(err),
		"created_at":     event.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	raw, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return json.RawMessage(`{"marshal_error":true}`)
	}
	return raw
}

func classifyHandlerFailure(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, ErrHandlerTimeout), errors.Is(err, context.DeadlineExceeded):
		return "handler_timeout"
	case errors.Is(err, context.Canceled):
		return "handler_canceled"
	case errors.Is(err, ErrInvalidEvent):
		return "handler_invalid_event"
	default:
		return "handler_error"
	}
}
