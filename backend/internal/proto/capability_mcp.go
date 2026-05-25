package proto

// MCPServerNode 是 mcp_server capability 的 payload。
type MCPServerNode struct {
	// ServerLabel 必填；对用户/审计可见的 MCP server 标签。
	ServerLabel string `json:"server_label"`

	// ServerURI 可选；server endpoint 或 logical URI，禁止写 secret。
	ServerURI string `json:"server_uri,omitempty"`

	// AllowedOperations 必填；可为空数组；列出允许的 MCP 操作。
	AllowedOperations []string `json:"allowed_operations"`

	// ApprovalRequired 必填；默认 false。
	ApprovalRequired bool `json:"approval_required"`

	// AuthRef 可选；凭据引用，禁止写真实 secret。
	AuthRef string `json:"auth_ref,omitempty"`

	// InvocationNodeIDs 可选；关联 tool_use invocation node id。
	InvocationNodeIDs []string `json:"invocation_node_ids,omitempty"`

	// ResultNodeIDs 可选；关联 tool_result node id。
	ResultNodeIDs []string `json:"result_node_ids,omitempty"`
}
