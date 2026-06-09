package proto

import "encoding/json"

// CanonicalRequest is the HCSF request envelope from spec sections 1-3.
//
// Passthrough（U7-B）：携带上游 / 客户端 JSON 中 HUAKAI 当前 typed 结构未
// 声明的字段。ClientAdapter 与 UpstreamAdapter 在 unmarshal 阶段用
// UnmarshalWithExtras 灌入，序列化阶段用 MergeExtrasInto 合并到输出。
// nil 表示无 unknown 字段（与既有行为兼容）。
type CanonicalRequest struct {
	Model             string             `json:"model"`
	Messages          []CanonicalMessage `json:"messages,omitempty"`
	Tools             []CanonicalTool    `json:"tools,omitempty"`
	ToolChoice        any                `json:"tool_choice,omitempty"`
	MaxTokens         int                `json:"max_tokens,omitempty"`
	StopSequences     []string           `json:"stop_sequences,omitempty"`
	Temperature       *float64           `json:"temperature,omitempty"`
	TopP              *float64           `json:"top_p,omitempty"`
	SystemPrompt      string             `json:"system_prompt,omitempty"`
	Stream            bool               `json:"stream"`
	ParallelToolCalls bool               `json:"parallel_tool_calls,omitempty"`
	ResponseFormat    map[string]any     `json:"response_format,omitempty"`

	// Passthrough 字段不上 wire（json:"-"）；由 helper 序列化时 merge。
	Passthrough *PassthroughEnvelope `json:"-"`
}

// CanonicalTool is an HCSF tool declaration carried by CanonicalRequest from spec sections 1-3.
type CanonicalTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// CanonicalMessage is an HCSF role-scoped message from spec sections 1-3.
type CanonicalMessage struct {
	Role    string                  `json:"role"`
	Content []CanonicalContentBlock `json:"content,omitempty"`
}

// CanonicalContentBlock is the HCSF tagged content union from spec sections 1-3.
type CanonicalContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// Thinking 保存 Anthropic buffered thinking block 的可见思考文本。
	Thinking string `json:"thinking,omitempty"`
	// Signature 保存 Anthropic thinking signature；只在协议允许路径透传。
	Signature string `json:"signature,omitempty"`
	// Data 保存 redacted_thinking 等上游定义的 opaque payload，保持 raw JSON。
	Data             json.RawMessage `json:"data,omitempty"`
	CallID           string          `json:"call_id,omitempty"`
	Name             string          `json:"name,omitempty"`
	Input            json.RawMessage `json:"input,omitempty"`
	ToolResult       json.RawMessage `json:"tool_result,omitempty"`
	Image            json.RawMessage `json:"image,omitempty"`
	ReasoningSummary string          `json:"reasoning_summary,omitempty"`
	// Raw 保存 unknown/empty content block 的原始 JSON，避免 silent drop。
	Raw json.RawMessage `json:"raw,omitempty"`
}

// CanonicalEvent is the HCSF streaming event union from spec sections 1-3.
//
// Passthrough（U7-B）：上游 vendor 在事件 JSON 顶层携带的 unknown 字段
// （system_fingerprint / service_tier / cache_creation_input_tokens 等）。
// nil 表示无 unknown 字段。
type CanonicalEvent struct {
	Type         string                 `json:"type"`
	MessageID    string                 `json:"message_id,omitempty"`
	Model        string                 `json:"model,omitempty"`
	Index        int                    `json:"index,omitempty"`
	ContentBlock *CanonicalContentBlock `json:"content_block,omitempty"`
	Delta        *CanonicalContentDelta `json:"delta,omitempty"`
	Usage        *CanonicalUsage        `json:"usage,omitempty"`
	StopReason   CanonicalStopReason    `json:"stop_reason,omitempty"`
	// NativeFinishReason preserves the upstream provider's raw streaming finish
	// reason when it differs from or is more specific than CanonicalStopReason.
	NativeFinishReason string `json:"native_finish_reason,omitempty"`

	Passthrough *PassthroughEnvelope `json:"-"`
}

// CanonicalContentDelta is the HCSF content_block_delta payload from spec sections 1-3.
type CanonicalContentDelta struct {
	Type          string          `json:"type"`
	Text          string          `json:"text,omitempty"`
	PartialJSON   json.RawMessage `json:"partial_json,omitempty"`
	ReasoningText string          `json:"reasoning_text,omitempty"`
	Signature     string          `json:"signature,omitempty"`
}

// CanonicalResponse is the HCSF buffered response envelope from spec sections 1-3.
//
// Passthrough（U7-B）：non-streaming 上游响应 JSON 顶层 unknown 字段。
// nil 表示无 unknown 字段。
type CanonicalResponse struct {
	ID         string                  `json:"id"`
	Model      string                  `json:"model"`
	Content    []CanonicalContentBlock `json:"content,omitempty"`
	Usage      CanonicalUsage          `json:"usage,omitempty"`
	StopReason CanonicalStopReason     `json:"stop_reason"`
	// StopSequence 保存 Anthropic stop_sequence 终止串；跨协议投影可选择降级。
	StopSequence string `json:"stop_sequence,omitempty"`

	Passthrough *PassthroughEnvelope `json:"-"`
}

// CanonicalUsage is the HCSF usage update payload from spec sections 1-3.
type CanonicalUsage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	TotalTokens  int `json:"total_tokens,omitempty"`

	// ReasoningTokens: 隐藏推理 token（OpenAI o1/o3 的
	// completion_tokens_details.reasoning_tokens）。它已被上游计入 OutputTokens，
	// 但对客户端不可见、估算器无法从可见内容数出。token 交叉校验须从 OutputTokens
	// 扣除它再与可见内容估算对比，否则推理重的合法响应会被误判为 usage 不一致
	// （纯审计明细，不参与计费/落账）。
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`

	// CacheCreationInputTokens: vendor 写入新缓存的 prompt token 数（首次见
	// 该 prefix）。Anthropic Messages API + Bedrock 同字段。命中率指标分母。
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	// CacheReadInputTokens: vendor 从已有缓存读出的 prompt token 数（命中）。
	// 命中率指标分子。
	CacheReadInputTokens int `json:"cache_read_input_tokens,omitempty"`
	// CacheCreationInputTokens5m/1h 保存 Anthropic cache_creation TTL 细分。
	// 它们是 CacheCreationInputTokens 的分项，不参与 TotalTokens 计算。
	CacheCreationInputTokens5m int `json:"cache_creation_input_tokens_5m,omitempty"`
	CacheCreationInputTokens1h int `json:"cache_creation_input_tokens_1h,omitempty"`

	// WebSearchCalls, FileSearchCalls, ImageGenerationCalls are billable
	// server-side built-in tool-call COUNTS (per call), populated by upstream
	// response parse in a later slice (Stage B+).
	//
	// IMPORTANT: these are call counts, NOT tokens. They MUST NOT enter the
	// UsageHasValue / token cross-check helper — doing so would cause spurious
	// usage-nonzero signals for token-less tool calls and break reconciliation.
	WebSearchCalls       int `json:"web_search_calls,omitempty"`
	FileSearchCalls      int `json:"file_search_calls,omitempty"`
	ImageGenerationCalls int `json:"image_generation_calls,omitempty"`
}

func UsageHasValue(usage CanonicalUsage) bool {
	return usage.InputTokens != 0 || usage.OutputTokens != 0 || usage.TotalTokens != 0 ||
		usage.CacheCreationInputTokens != 0 || usage.CacheReadInputTokens != 0 ||
		usage.CacheCreationInputTokens5m != 0 || usage.CacheCreationInputTokens1h != 0
}

// CanonicalStopReason is the HCSF stop reason enum from spec sections 1-3.
type CanonicalStopReason string

const (
	CanonicalStopEndTurn   CanonicalStopReason = "end_turn"
	CanonicalStopMaxTokens CanonicalStopReason = "max_tokens"
	CanonicalStopSequence  CanonicalStopReason = "stop_sequence"
	CanonicalStopToolUse   CanonicalStopReason = "tool_use"
	CanonicalStopRefusal   CanonicalStopReason = "refusal"
	CanonicalStopUnknown   CanonicalStopReason = "unknown"
)
