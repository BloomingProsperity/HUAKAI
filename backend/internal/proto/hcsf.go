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
	Type             string          `json:"type"`
	Text             string          `json:"text,omitempty"`
	CallID           string          `json:"call_id,omitempty"`
	Name             string          `json:"name,omitempty"`
	Input            json.RawMessage `json:"input,omitempty"`
	ToolResult       json.RawMessage `json:"tool_result,omitempty"`
	Image            json.RawMessage `json:"image,omitempty"`
	ReasoningSummary string          `json:"reasoning_summary,omitempty"`
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

	Passthrough *PassthroughEnvelope `json:"-"`
}

// CanonicalUsage is the HCSF usage update payload from spec sections 1-3.
type CanonicalUsage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	TotalTokens  int `json:"total_tokens,omitempty"`
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
