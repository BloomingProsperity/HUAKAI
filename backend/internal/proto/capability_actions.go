package proto

import "encoding/json"

// ToolNodeStatus 标记工具节点的处理阶段。
type ToolNodeStatus string

const (
	ToolNodePending  ToolNodeStatus = "pending"
	ToolNodePartial  ToolNodeStatus = "partial"
	ToolNodeComplete ToolNodeStatus = "complete"
	ToolNodeError    ToolNodeStatus = "error"
)

// ToolUseNode 是 tool_use capability 的 payload。
type ToolUseNode struct {
	ToolCallID         string          `json:"tool_call_id"`
	OriginalToolCallID string          `json:"original_tool_call_id,omitempty"`
	Name               string          `json:"name"`
	DisplayName        string          `json:"display_name,omitempty"`
	Input              json.RawMessage `json:"input"`
	PartialInput       json.RawMessage `json:"partial_input,omitempty"`
	Status             ToolNodeStatus  `json:"status"`
}

// ToolResultNode 是 tool_result capability 的 payload。
type ToolResultNode struct {
	ToolCallID string                  `json:"tool_call_id"`
	Content    []CanonicalContentBlock `json:"content"`
	Status     ToolNodeStatus          `json:"status"`
	IsError    bool                    `json:"is_error"`
}

// MCPServerNode 是 mcp_server capability 的 payload。
type MCPServerNode struct {
	ServerLabel       string   `json:"server_label"`
	ServerURI         string   `json:"server_uri,omitempty"`
	AllowedOperations []string `json:"allowed_operations"`
	ApprovalRequired  bool     `json:"approval_required"`
	AuthRef           string   `json:"auth_ref,omitempty"`
	InvocationNodeIDs []string `json:"invocation_node_ids,omitempty"`
	ResultNodeIDs     []string `json:"result_node_ids,omitempty"`
}

// ApprovalState 标记 computer_use action 的审批状态。
type ApprovalState string

const (
	ApprovalRequired    ApprovalState = "required"
	ApprovalGranted     ApprovalState = "granted"
	ApprovalDenied      ApprovalState = "denied"
	ApprovalNotRequired ApprovalState = "not_required"
)

// ComputerUseNode 是 computer_use capability 的 payload。
type ComputerUseNode struct {
	Environment   string          `json:"environment"`
	Action        string          `json:"action"`
	Input         json.RawMessage `json:"input,omitempty"`
	ScreenshotRef string          `json:"screenshot_ref,omitempty"`
	Approval      ApprovalState   `json:"approval"`
	AuditLabel    string          `json:"audit_label,omitempty"`
}
