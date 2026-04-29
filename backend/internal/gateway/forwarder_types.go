package gateway

import (
	"errors"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// StreamEndClass is the F-GW-002 Phase C closed stream-end taxonomy.
type StreamEndClass string

const (
	StreamEndGraceful     StreamEndClass = "stream_end_graceful"
	UpstreamEOFNoTerminal StreamEndClass = "upstream_eof_no_terminal"
	UpstreamError4xx      StreamEndClass = "upstream_error_4xx"
	UpstreamError5xx      StreamEndClass = "upstream_error_5xx"
	UpstreamRateLimit     StreamEndClass = "upstream_rate_limit"
	UpstreamAuthFailure   StreamEndClass = "upstream_auth_failure"
	FirstTokenTimeout     StreamEndClass = "first_token_timeout"
	InterEventTimeout     StreamEndClass = "inter_event_timeout"
	TotalStreamTimeout    StreamEndClass = "total_stream_timeout"
	ClientDisconnect      StreamEndClass = "client_disconnect"
	ResponseEventTooLarge StreamEndClass = "response_event_too_large"
	OrchestratorCancel    StreamEndClass = "orchestrator_cancel"
	AmbiguousUsage        StreamEndClass = "ambiguous_usage"
	UnknownTermination    StreamEndClass = "unknown_termination"
)

// UsageSource is the F-GW-002 Phase B usage trust source enum.
type UsageSource string

const (
	UsageSourceReported   UsageSource = "reported"
	UsageSourceNormalized UsageSource = "normalized"
	UsageSourceInferred   UsageSource = "inferred"
	UsageSourcePartial    UsageSource = "partial"
	UsageSourceAmbiguous  UsageSource = "ambiguous"
)

// DrainOutcome is the F-GW-002 Phase C-bis bounded drain result enum.
type DrainOutcome string

const (
	DrainBudgetSecondsExhausted DrainOutcome = "budget_seconds_exhausted"
	DrainBudgetBytesExhausted   DrainOutcome = "budget_bytes_exhausted"
	DrainBudgetCostExhausted    DrainOutcome = "budget_cost_exhausted"
	DrainNotDrained             DrainOutcome = "not_drained"
)

// TimeoutConfig carries the F-GW-002 eight-axis timeout configuration.
type TimeoutConfig struct {
	FirstTokenTimeout   time.Duration `json:"first_token_timeout"`
	InterEventTimeout   time.Duration `json:"inter_event_timeout"`
	TotalStreamTimeout  time.Duration `json:"total_stream_timeout"`
	IdleAfterTerminal   time.Duration `json:"idle_after_terminal"`
	DrainMaxSeconds     time.Duration `json:"drain_max_seconds"`
	ScannerReadTimeout  time.Duration `json:"scanner_read"`
	HeaderToFirstByte   time.Duration `json:"header_to_first_byte"`
	RequestTotalTimeout time.Duration `json:"request_total"`
}

// DrainBudgets carries the F-GW-002 Phase C-bis drain guardrails.
type DrainBudgets struct {
	MaxSeconds       time.Duration   `json:"max_seconds"`
	MaxBytes         int64           `json:"max_bytes"`
	MaxEstimatedCost decimal.Decimal `json:"max_estimated_cost"`
}

// UsageRecordDraft is the F-GW-002 Phase D handoff payload for F-OBS-001 Tx2.
type UsageRecordDraft struct {
	TokensInput             int             `json:"tokens_input"`
	TokensOutput            int             `json:"tokens_output"`
	CacheCreationTokens     int             `json:"cache_creation_tokens"`
	CacheReadTokens         int             `json:"cache_read_tokens"`
	ActualCost              decimal.Decimal `json:"actual_cost"`
	RoutingReason           []byte          `json:"routing_reason"`
	EndClass                StreamEndClass  `json:"end_class"`
	UsageSource             UsageSource     `json:"usage_source"`
	ConfidenceScore         *float64        `json:"confidence_score"`
	DrainOutcome            DrainOutcome    `json:"drain_outcome"`
	PendingReconciliation   bool            `json:"pending_reconciliation"`
	FirstTokenLatencyMillis int64           `json:"first_token_latency_ms"`
	TotalDurationMillis     int64           `json:"total_duration_ms"`
}

// ForwardRequest carries the F-GW-002 request identity and protocol metadata.
type ForwardRequest struct {
	TenantID             int64     `json:"tenant_id"`
	AccountID            int64     `json:"account_id"`
	AcquisitionToken     uuid.UUID `json:"acquisition_token"`
	RouteID              string    `json:"route_id"`
	UpstreamProtocol     string    `json:"upstream_protocol"`
	ClientProtocol       string    `json:"client_protocol"`
	Model                string    `json:"model"`
	RoutingReasonPayload []byte    `json:"routing_reason_payload"`
}

// UsageAccumulator tracks F-GW-002 Phase B per-source usage signals.
// After Freeze() is called (on terminal-frame observation per spec AT-15),
// Update() applies the next signal one final time and refuses subsequent
// updates so the terminal frame's authority is preserved.
type UsageAccumulator struct {
	Usage          proto.CanonicalUsage `json:"usage"`
	Source         UsageSource          `json:"source"`
	TerminalLocked bool                 `json:"terminal_locked"`
}

// Update merges a F-GW-002 Phase B usage signal. When TerminalLocked is true,
// the update is dropped per spec AT-15 (terminal frame wins over later signals).
func (a *UsageAccumulator) Update(source UsageSource, usage proto.CanonicalUsage) {
	if a.TerminalLocked {
		return
	}
	if usage.InputTokens != 0 {
		a.Usage.InputTokens = usage.InputTokens
	}
	if usage.OutputTokens != 0 {
		a.Usage.OutputTokens = usage.OutputTokens
	}
	if usage.TotalTokens != 0 {
		a.Usage.TotalTokens = usage.TotalTokens
	}
	if a.Usage.TotalTokens == 0 {
		a.Usage.TotalTokens = a.Usage.InputTokens + a.Usage.OutputTokens
	}
	if source != "" {
		a.Source = source
	}
}

// Freeze locks the accumulator after a terminal frame is observed (spec §Phase B AT-15).
func (a *UsageAccumulator) Freeze() { a.TerminalLocked = true }

// Empty reports whether F-GW-002 Phase D has no billable usage signal.
func (a UsageAccumulator) Empty() bool {
	return a.Usage.InputTokens == 0 && a.Usage.OutputTokens == 0 && a.Usage.TotalTokens == 0
}

var (
	ErrScannerOverflow    = errors.New("gateway: scanner event overflow")
	ErrFirstTokenTimeout  = errors.New("gateway: first token timeout")
	ErrInterEventTimeout  = errors.New("gateway: inter-event timeout")
	ErrTotalStreamTimeout = errors.New("gateway: total stream timeout")
	ErrAmbiguousUsage     = errors.New("gateway: ambiguous usage")
	ErrClientDisconnect   = errors.New("gateway: client disconnect")
)
