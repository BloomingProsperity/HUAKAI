package proto

import "encoding/json"

// LiveTransport 标记 live_session 传输协议。
type LiveTransport string

const (
	LiveTransportWSS LiveTransport = "wss"
	LiveTransportSSE LiveTransport = "sse"
)

// LiveSessionNode 是 live_session capability 的 payload。
type LiveSessionNode struct {
	// SessionID 必填；HUAKAI 或 provider session id。
	SessionID string `json:"session_id"`

	// Transport 必填；wss/sse。
	Transport LiveTransport `json:"transport"`

	// ConnectParams 可选；provider-specific 连接参数。
	ConnectParams json.RawMessage `json:"connect_params,omitempty"`

	// Modalities 必填；可为空数组；如 text/audio/video。
	Modalities []string `json:"modalities"`

	// ToolNodeIDs 可选；live session 可用的 tool/computer/mcp node id。
	ToolNodeIDs []string `json:"tool_node_ids,omitempty"`

	// ResumeToken 可选；session resume hook。
	ResumeToken string `json:"resume_token,omitempty"`

	// CloseReason 可选；session 关闭原因。
	CloseReason string `json:"close_reason,omitempty"`
}
