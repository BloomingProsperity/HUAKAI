package proto

// StreamMode 声明 envelope 的流式形态。
//
//   - buffered：非流式；BufferedResponse 必填，StreamEvents 必空。
//   - streaming：流式；StreamEvents 在运行时按事件推送（envelope 不强制缓存）。
//   - replay：测试 / fixture 重放；StreamEvents 列出全量事件。
type StreamMode string

const (
	StreamModeBuffered  StreamMode = "buffered"
	StreamModeStreaming StreamMode = "streaming"
	StreamModeReplay    StreamMode = "replay"
)

// FallbackBoundary 表达跨上游 fallback 的安全边界。P-0 默认 after_first_byte_blocked。
type FallbackBoundary string

const (
	// FallbackBeforeFirstByte 首字节前可任意切换上游。
	FallbackBeforeFirstByte FallbackBoundary = "before_first_byte"

	// FallbackAfterFirstByteBlocked 首字节后禁止切换上游（P-0 默认；安全保守）。
	FallbackAfterFirstByteBlocked FallbackBoundary = "after_first_byte_blocked"

	// FallbackAfterFirstByteAllowed 首字节后允许切换上游（P-8 mid-stream fallback 启用后）。
	FallbackAfterFirstByteAllowed FallbackBoundary = "after_first_byte_allowed"
)

// MidStreamFallbackPolicy 是 D9 推迟决策的 schema 留位；P-0 默认 none，P-8 才能启用。
type MidStreamFallbackPolicy string

const (
	// MidStreamFallbackNone P-0 默认；中段错误直接终止流并归一化为 ProtocolLossEntry。
	MidStreamFallbackNone MidStreamFallbackPolicy = "none"

	// MidStreamFallbackContinuation 切换至 fallback 上游 + continuation prompt 续接（P-8 roadmap）。
	MidStreamFallbackContinuation MidStreamFallbackPolicy = "continuation"

	// MidStreamFallbackRestart 切换至 fallback 上游 + 用原始 messages 重新开始（P-8 roadmap）。
	MidStreamFallbackRestart MidStreamFallbackPolicy = "restart"
)

// StreamPlan 描述 envelope 的流式策略；非流式时 Mode=buffered。
type StreamPlan struct {
	// Mode 必填；buffered/streaming/replay。
	Mode StreamMode `json:"mode"`

	// EventClasses 必填；可为空数组；声明本流允许的 CanonicalEvent.Type 集合。
	EventClasses []string `json:"event_classes"`

	// FlushPolicy 必填；如 per_event / batch_chunked / coalesced。
	FlushPolicy string `json:"flush_policy"`

	// TerminalRequired 必填；终止事件是否必须出现（如 message_stop）。
	TerminalRequired bool `json:"terminal_required"`

	// SyntheticTerminalAllowed 必填；上游缺失终止事件时是否允许合成。
	SyntheticTerminalAllowed bool `json:"synthetic_terminal_allowed"`

	// FallbackBoundary 必填；P-0 默认 after_first_byte_blocked。
	FallbackBoundary FallbackBoundary `json:"fallback_boundary"`

	// MidStreamFallbackPolicy 必填；P-0 默认 none（D9 推迟，P-8 才能改）。
	MidStreamFallbackPolicy MidStreamFallbackPolicy `json:"mid_stream_fallback_policy"`

	// Recoverable 可选；P-8 mid-stream fallback hook；P-0 默认 false。
	Recoverable bool `json:"recoverable,omitempty"`
}
