package proto

import "encoding/json"

// ToolNodeStatus 标记 tool node 的处理阶段。
type ToolNodeStatus string

const (
	ToolNodePending  ToolNodeStatus = "pending"
	ToolNodePartial  ToolNodeStatus = "partial"
	ToolNodeComplete ToolNodeStatus = "complete"
	ToolNodeError    ToolNodeStatus = "error"
)

// ToolUseNode 是 tool_use capability 的 payload。
type ToolUseNode struct {
	// ToolCallID 必填；canonical id；D4 未决前不固定 SHA-8/SHA-12 长度。
	ToolCallID string `json:"tool_call_id"`

	// OriginalToolCallID 可选；保留上游原始 id，禁止泄露到不兼容客户端。
	OriginalToolCallID string `json:"original_tool_call_id,omitempty"`

	// Name 必填；规范化 tool name。
	Name string `json:"name"`

	// DisplayName 可选；面向客户端展示，可被 provider projection 截断。
	DisplayName string `json:"display_name,omitempty"`

	// Input 必填；必须是 JSON object 或 null；partial delta 拼接完成前不得伪造成完成态。
	Input json.RawMessage `json:"input"`

	// PartialInput 可选；streaming partial argument delta。
	PartialInput json.RawMessage `json:"partial_input,omitempty"`

	// Status 必填；pending/partial/complete/error。
	Status ToolNodeStatus `json:"status"`
}

// ToolResultNode 是 tool_result capability 的 payload。
type ToolResultNode struct {
	// ToolCallID 必填；必须通过 EdgeRequires 指向 ToolUseNode。
	ToolCallID string `json:"tool_call_id"`

	// Content 必填；可为空数组；复用 CanonicalContentBlock。
	Content []CanonicalContentBlock `json:"content"`

	// Status 必填；complete/error。
	Status ToolNodeStatus `json:"status"`

	// IsError 必填；默认 false。
	IsError bool `json:"is_error"`
}
