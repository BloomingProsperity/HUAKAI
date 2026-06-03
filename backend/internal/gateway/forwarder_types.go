// 注意：本文件是 PATCH 提案文件（_patch.go 后缀），不参与 Go build。
// 将 ForwardRequest 的修改部分合并到 backend/internal/gateway/forwarder_types.go。
//
// 唯一变更：ForwardRequest 新增 ProtocolFamily 字段。
// 其余所有字段（TenantID / AccountID / AcquisitionToken / RouteID /
// UpstreamProtocol / ClientProtocol / Model / RoutingReasonPayload）原样保留。
package gateway

import (
	"errors"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// StreamEndClass 是 F-GW-002 Phase C 封闭流结束分类枚举。
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

// UsageSource 是 F-GW-002 Phase B usage 信任来源枚举。
type UsageSource string

const (
	UsageSourceReported   UsageSource = "reported"
	UsageSourceNormalized UsageSource = "normalized"
	UsageSourceInferred   UsageSource = "inferred"
	UsageSourcePartial    UsageSource = "partial"
	UsageSourceAmbiguous  UsageSource = "ambiguous"
)

// DrainOutcome 是 F-GW-002 Phase C-bis 有界 drain 结果枚举。
type DrainOutcome string

const (
	DrainBudgetSecondsExhausted DrainOutcome = "budget_seconds_exhausted"
	DrainBudgetBytesExhausted   DrainOutcome = "budget_bytes_exhausted"
	DrainBudgetCostExhausted    DrainOutcome = "budget_cost_exhausted"
	DrainNotDrained             DrainOutcome = "not_drained"
)

// TimeoutConfig 携带 F-GW-002 八轴超时配置。
type TimeoutConfig struct {
	FirstTokenTimeout   time.Duration `json:"first_token_timeout"`
	InterEventTimeout   time.Duration `json:"inter_event_timeout"`
	TotalStreamTimeout  time.Duration `json:"total_stream_timeout"`
	IdleAfterTerminal   time.Duration `json:"idle_after_terminal"`
	DrainMaxSeconds     time.Duration `json:"drain_max_seconds"`
	ScannerReadTimeout  time.Duration `json:"scanner_read"`
	HeaderToFirstByte   time.Duration `json:"header_to_first_byte"`
	RequestTotalTimeout time.Duration `json:"request_total"`
	// KeepAliveInterval 控制空闲时向客户端发送 SSE 注释心跳(": hk\n\n")的间隔,用于在长 TTFT /
	// 稀疏 token 间隙保持连接活跃,避免 Cloudflare 等反代 ~100s 空闲超时断链(524)。0 = 关闭。
	// 应显著小于反代空闲阈值(默认 15s)。心跳不影响 First/Inter/Total 这些"上游静默即放弃"的检测。
	KeepAliveInterval time.Duration `json:"keep_alive_interval"`
}

// DrainBudgets 携带 F-GW-002 Phase C-bis drain 护栏参数。
type DrainBudgets struct {
	MaxSeconds       time.Duration   `json:"max_seconds"`
	MaxBytes         int64           `json:"max_bytes"`
	MaxEstimatedCost decimal.Decimal `json:"max_estimated_cost"`
}

// UsageRecordDraft 是 F-GW-002 Phase D 交给 F-OBS-001 Tx2 的载荷。
type UsageRecordDraft struct {
	TokensInput             int             `json:"tokens_input"`
	TokensOutput            int             `json:"tokens_output"`
	DeliveredTokenCount     int64           `json:"delivered_token_count"`
	CacheCreationTokens     int             `json:"cache_creation_tokens"`
	CacheCreation5mTokens   int             `json:"cache_creation_5m_tokens"`
	CacheCreation1hTokens   int             `json:"cache_creation_1h_tokens"`
	CacheReadTokens         int             `json:"cache_read_tokens"`
	ActualCost              decimal.Decimal `json:"actual_cost"`
	CostSnapshot            string          `json:"cost_snapshot,omitempty"`
	CacheCreationCost       decimal.Decimal `json:"cache_creation_cost"`
	CacheReadCost           decimal.Decimal `json:"cache_read_cost"`
	RoutingReason           []byte          `json:"routing_reason"`
	EndClass                StreamEndClass  `json:"end_class"`
	StreamTerminatedReason  string          `json:"stream_terminated_reason"`
	UsageSource             UsageSource     `json:"usage_source"`
	ConfidenceScore         *float64        `json:"confidence_score"`
	DrainOutcome            DrainOutcome    `json:"drain_outcome"`
	PendingReconciliation   bool            `json:"pending_reconciliation"`
	FirstTokenLatencyMillis int64           `json:"first_token_latency_ms"`
	TotalDurationMillis     int64           `json:"total_duration_ms"`

	// ReasoningTokens / EstimatedOutputTokens / EstimatedReasoningTokens 携带流式 token 交叉校验
	// 所需信号到 gatewayhttp 层(settle 时与 reported OutputTokens 比对,审计-only,不参与计费):
	// ReasoningTokens = 上游报告的隐藏 reasoning（已计入 TokensOutput,交叉校验须扣除）；
	// EstimatedOutputTokens = forwarder 逐事件累加的可见输出启发式估算；
	// EstimatedReasoningTokens = 逐事件累加的可见 reasoning 文本估算,仅用于判断 reasoning-folding
	// 不可知时跳过交叉校验避免误报(S2-163-fu review R2),不参与计费(S2-163-fu)。
	ReasoningTokens          int `json:"reasoning_tokens,omitempty"`
	EstimatedOutputTokens    int `json:"estimated_output_tokens,omitempty"`
	EstimatedReasoningTokens int `json:"estimated_reasoning_tokens,omitempty"`

	// StreamProtocolLoss 累积流式逐事件的协议损失(provider→canonical 与
	// canonical→client chunk 转换)。settler / SettleRequest 绝不可直接读它 ——
	// 由 chat_completions_stream.go 的 streamingCompletionEvent 合并进
	// SettleRequest.ProtocolLoss。命名为 StreamProtocolLoss(而非 ProtocolLoss)是
	// 为避免复活 S1-025 已删除的死字段 Draft.ProtocolLoss(settler 曾误读它)。
	StreamProtocolLoss []proto.ProtocolLossEntry `json:"stream_protocol_loss,omitempty"`
}

// ForwardRequest 携带 F-GW-002 请求身份和协议元数据。
//
// 新增字段（wire-up 重构）：
//   - ProtocolFamily：协议族标识符，作为 ProtocolAdapterRegistry.For() 的查询键。
//     调用方必须明确填写；空值会触发 ErrUnknownProtocolFamily 错误。
//     合法值示例："anthropic_messages" / "openai_chat" / "openai_responses" / "gemini_messages"。
//
// 保留字段：TenantID / AccountID / AcquisitionToken / RouteID /
//
//	UpstreamProtocol / ClientProtocol / Model / RoutingReasonPayload。
type ForwardRequest struct {
	TenantID         int64     `json:"tenant_id"`
	AccountID        int64     `json:"account_id"`
	AcquisitionToken uuid.UUID `json:"acquisition_token"`
	RequestID        string    `json:"request_id"`
	RouteID          string    `json:"route_id"`
	PoolID           string    `json:"pool_id"`
	IngressPath      string    `json:"ingress_path"`

	// ProtocolFamily 指定上游协议族，用于从 ProtocolAdapterRegistry 查询对应 adapter。
	// 必须非空；Forward 在入口处校验，空值返回 ErrUnknownProtocolFamily 封装的错误。
	ProtocolFamily string `json:"protocol_family"`

	// UpstreamProtocol / ClientProtocol 保留作向后兼容；
	// 新代码应通过 ProtocolFamily 驱动 adapter 选择，而非这两个字段。
	UpstreamProtocol     string `json:"upstream_protocol"`
	ClientProtocol       string `json:"client_protocol"`
	Model                string `json:"model"`
	RequestedModel       string `json:"requested_model"`
	Provider             string `json:"provider"`
	RoutingReasonPayload []byte `json:"routing_reason_payload"`

	// SessionHash（PASR-lite A4）: 上游已 hash 的 prompt prefix, 流入 proto
	// adapter UpstreamState.PrefixHash, 终态调
	// cachemetrics.ObserveByAccountWithPrefix 让 PASR observer 收反馈。
	// 空串 → PASR 路径退化, 仅 per-account counter 累积。
	SessionHash string `json:"session_hash"`
}

// UsageAccumulator 跟踪 F-GW-002 Phase B 各来源 usage 信号。
// Freeze() 被调用后（终态帧观测，规格 AT-15），Update() 仅再接受一次信号，
// 此后拒绝后续更新，保留终态帧权威性。
type UsageAccumulator struct {
	Usage               proto.CanonicalUsage `json:"usage"`
	Source              UsageSource          `json:"source"`
	TerminalLocked      bool                 `json:"terminal_locked"`
	DeliveredChunkCount int64                `json:"delivered_chunk_count"`
	// EstimatedOutputTokens 逐事件增量累加的**可见**输出 token 启发式估算
	// (tokencheck.EstimateStreamDelta,排除隐藏 reasoning)。finishDraft 拷入 draft,
	// settle 时与 reported OutputTokens 交叉校验,仅作审计信号(S2-163-fu)。不滞留响应内容。
	EstimatedOutputTokens int `json:"estimated_output_tokens,omitempty"`
	// EstimatedReasoningTokens 逐事件累加的可见 reasoning 文本(Delta.ReasoningText)估算,
	// 用于 settle 时判断 reasoning-folding 是否可知:reasoning 文本流出但 ReasoningTokens 缺失 →
	// folding 不可知 → 跳过交叉校验避免误报(S2-163-fu review R2)。不滞留响应内容。
	EstimatedReasoningTokens int `json:"estimated_reasoning_tokens,omitempty"`
	// StreamProtocolLoss 累积逐事件协议损失,finishDraft 拷入 UsageRecordDraft。
	StreamProtocolLoss []proto.ProtocolLossEntry `json:"stream_protocol_loss,omitempty"`
}

// Update 合并 F-GW-002 Phase B usage 信号。
// TerminalLocked 为 true 时丢弃更新（规格 AT-15：终态帧优先）。
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
	if usage.ReasoningTokens != 0 {
		a.Usage.ReasoningTokens = usage.ReasoningTokens
	}
	if usage.TotalTokens != 0 {
		a.Usage.TotalTokens = usage.TotalTokens
	}
	if usage.CacheCreationInputTokens != 0 {
		a.Usage.CacheCreationInputTokens = usage.CacheCreationInputTokens
	}
	if usage.CacheCreationInputTokens5m != 0 {
		a.Usage.CacheCreationInputTokens5m = usage.CacheCreationInputTokens5m
	}
	if usage.CacheCreationInputTokens1h != 0 {
		a.Usage.CacheCreationInputTokens1h = usage.CacheCreationInputTokens1h
	}
	if usage.CacheReadInputTokens != 0 {
		a.Usage.CacheReadInputTokens = usage.CacheReadInputTokens
	}
	if a.Usage.TotalTokens == 0 {
		a.Usage.TotalTokens = a.Usage.InputTokens + a.Usage.OutputTokens
	}
	if source != "" {
		a.Source = source
	}
}

// Freeze 在终态帧观测后锁定累加器（规格 §Phase B AT-15）。
func (a *UsageAccumulator) Freeze() { a.TerminalLocked = true }

// Empty 报告 F-GW-002 Phase D 是否没有可计费 usage 信号。
func (a UsageAccumulator) Empty() bool {
	return a.Usage.InputTokens == 0 && a.Usage.OutputTokens == 0 && a.Usage.TotalTokens == 0 &&
		a.Usage.CacheCreationInputTokens == 0 && a.Usage.CacheCreationInputTokens5m == 0 &&
		a.Usage.CacheCreationInputTokens1h == 0 && a.Usage.CacheReadInputTokens == 0
}

func (a UsageAccumulator) DeliveredTokenCount() int64 {
	if a.Usage.OutputTokens > 0 {
		return int64(a.Usage.OutputTokens)
	}
	if a.DeliveredChunkCount > 0 {
		return a.DeliveredChunkCount
	}
	return 0
}

var (
	ErrScannerOverflow    = errors.New("gateway: scanner event overflow")
	ErrFirstTokenTimeout  = errors.New("gateway: first token timeout")
	ErrInterEventTimeout  = errors.New("gateway: inter-event timeout")
	ErrTotalStreamTimeout = errors.New("gateway: total stream timeout")
	ErrAmbiguousUsage     = errors.New("gateway: ambiguous usage")
	ErrClientDisconnect   = errors.New("gateway: client disconnect")

	// ErrNilProtocolAdapterRegistry 表示 StreamForwarder.ProtocolAdapters 未注入。
	// 调用方应在构造 StreamForwarder 时注入非 nil 的注册表。
	ErrNilProtocolAdapterRegistry = errors.New("gateway: ProtocolAdapters 注册表未注入（nil）")
)
