package proto

import "encoding/json"

// Anthropic Messages API 线格式类型 — 仅本包 internal 用，不对外暴露。
//
// 字段子集覆盖 P-2 D1 + D1.x 所需：text / system / tools / tool_choice /
// tool_use / tool_result / image / thinking / cache_control。

// anthropicMessagesRequest 是 Anthropic Messages body 子集。
type anthropicMessagesRequest struct {
	Model             string                     `json:"model"`
	MaxTokens         *int                       `json:"max_tokens"`
	MaxTokensToSample *int                       `json:"max_tokens_to_sample,omitempty"`
	Messages          []anthropicMessage         `json:"messages"`
	System            json.RawMessage            `json:"system,omitempty"`
	Stream            *bool                      `json:"stream"`
	Temperature       *float64                   `json:"temperature"`
	TopP              *float64                   `json:"top_p"`
	TopK              json.RawMessage            `json:"top_k,omitempty"`
	StopSequences     []string                   `json:"stop_sequences,omitempty"`
	Tools             []json.RawMessage          `json:"tools,omitempty"`
	ToolChoice        json.RawMessage            `json:"tool_choice,omitempty"`
	Metadata          map[string]json.RawMessage `json:"metadata,omitempty"`
	Thinking          json.RawMessage            `json:"thinking,omitempty"`
	ContextManagement json.RawMessage            `json:"context_management,omitempty"`
	OutputConfig      json.RawMessage            `json:"output_config,omitempty"`
	OutputFormat      json.RawMessage            `json:"output_format,omitempty"`
	Container         json.RawMessage            `json:"container,omitempty"`
}

// anthropicMessage 是 messages[] 数组元素。
type anthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// anthropicContentBlock 覆盖 Anthropic content array 全部 type。
type anthropicContentBlock struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text,omitempty"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
	Raw          json.RawMessage        `json:"-"`
	// tool_use 字段
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result 字段
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	// image 字段：source 含 type/media_type/data 等
	Source *anthropicImageSource `json:"source,omitempty"`
}

// anthropicCacheControl 是 cache_control breakpoint marker；Anthropic 当前
// 只有 type=ephemeral。
type anthropicCacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

// anthropicImageSource 是 Anthropic content block 中 image 的 source 子字段。
type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

// anthropicThinkingConfig 是顶层 thinking 字段。
type anthropicThinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}
