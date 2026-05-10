package proto

// CapabilityKind 是 14 capability families 的 string enum。
//
// 计数口径锁定 14 个 capability：text / tool（含 tool_use + tool_result 两个 concrete node）/
// thinking / cache_control / structured_output / computer_use / file / image / audio / video /
// live_session / batch / mcp_server / data_retention。
//
// 不引入 multimodal 聚合 family —— file/image/audio/video 的 lifecycle 与 issue 模式都不同。
// tool_use + tool_result 是同一 family 的两个 node kind，让 tool 链路可寻址。
type CapabilityKind string

const (
	CapabilityText             CapabilityKind = "text"
	CapabilityToolUse          CapabilityKind = "tool_use"
	CapabilityToolResult       CapabilityKind = "tool_result"
	CapabilityThinking         CapabilityKind = "thinking"
	CapabilityCacheControl     CapabilityKind = "cache_control"
	CapabilityStructuredOutput CapabilityKind = "structured_output"
	CapabilityComputerUse      CapabilityKind = "computer_use"
	CapabilityFile             CapabilityKind = "file"
	CapabilityImage            CapabilityKind = "image"
	CapabilityAudio            CapabilityKind = "audio"
	CapabilityVideo            CapabilityKind = "video"
	CapabilityLiveSession      CapabilityKind = "live_session"
	CapabilityBatch            CapabilityKind = "batch"
	CapabilityMCPServer        CapabilityKind = "mcp_server"
	CapabilityDataRetention    CapabilityKind = "data_retention"
)

// AllCapabilityKinds 列出所有合法 capability kind，envelope_validate 与测试遍历用。
var AllCapabilityKinds = []CapabilityKind{
	CapabilityText,
	CapabilityToolUse,
	CapabilityToolResult,
	CapabilityThinking,
	CapabilityCacheControl,
	CapabilityStructuredOutput,
	CapabilityComputerUse,
	CapabilityFile,
	CapabilityImage,
	CapabilityAudio,
	CapabilityVideo,
	CapabilityLiveSession,
	CapabilityBatch,
	CapabilityMCPServer,
	CapabilityDataRetention,
}

// StreamReadiness 是 capability node 在 streaming 路径上的能力声明。
type StreamReadiness string

const (
	StreamReadyYes     StreamReadiness = "yes"
	StreamReadyNo      StreamReadiness = "no"
	StreamReadyPartial StreamReadiness = "partial"
)

// CapabilityGraph 承载 14 capability families 的 node/edge/loss 表达。
type CapabilityGraph struct {
	// Nodes 必填；可为空数组；empty graph 是合法 fixture。
	Nodes []CapabilityNode `json:"nodes"`

	// Edges 必填；可为空数组；node 间因果、依赖、usage、policy 关系写这里。
	Edges []CapabilityEdge `json:"edges"`

	// ProtocolLoss 可选；图级 loss，node/projection 更具体时优先写到更具体位置。
	ProtocolLoss []ProtocolLossEntry `json:"protocol_loss,omitempty"`
}

// NodeSourceRef 指向 messages/events/request field 的来源位置。
type NodeSourceRef struct {
	// MessageIndex 可选；0 是有效值，所以用 pointer。
	MessageIndex *int `json:"message_index,omitempty"`

	// BlockIndex 可选；0 是有效值，所以用 pointer。
	BlockIndex *int `json:"block_index,omitempty"`

	// EventIndex 可选；用于 StreamEvents fixture。
	EventIndex *int `json:"event_index,omitempty"`

	// RequestField 可选；用于 request_controls 或 policy 派生节点。
	RequestField string `json:"request_field,omitempty"`
}

// CapabilityNode 是 tagged-union 形态：Kind 决定 14 个 nullable payload pointer 中哪一个非空。
//
// 一致性约束（INV-3）：Kind=="text" ⟺ Text!=nil ⟺ 其它 14 nullable pointer 全 nil。
// envelope_validate 严格执行。
type CapabilityNode struct {
	// ID 必填；envelope 内唯一；建议格式 n_<kind>_<seq>，不含 vendor 前缀。
	ID string `json:"id"`

	// Kind 必填；必须与下方恰好一个 payload 字段对应。
	Kind CapabilityKind `json:"kind"`

	// Source 可选；指向 messages/events/request field 的来源位置。
	Source *NodeSourceRef `json:"source,omitempty"`

	// StreamReady 必填；取 yes/no/partial；用于 matrix 与 StreamPlan 校验。
	StreamReady StreamReadiness `json:"stream_ready"`

	// ProtocolLoss 可选；该 capability 自身产生的 loss。
	ProtocolLoss []ProtocolLossEntry `json:"protocol_loss,omitempty"`

	// 14 个 nullable payload pointer：Kind 决定哪个非空。

	Text             *TextNode             `json:"text,omitempty"`
	ToolUse          *ToolUseNode          `json:"tool_use,omitempty"`
	ToolResult       *ToolResultNode       `json:"tool_result,omitempty"`
	Thinking         *ThinkingNode         `json:"thinking,omitempty"`
	CacheControl     *CacheControlNode     `json:"cache_control,omitempty"`
	StructuredOutput *StructuredOutputNode `json:"structured_output,omitempty"`
	ComputerUse      *ComputerUseNode      `json:"computer_use,omitempty"`
	File             *FileNode             `json:"file,omitempty"`
	Image            *ImageNode            `json:"image,omitempty"`
	Audio            *AudioNode            `json:"audio,omitempty"`
	Video            *VideoNode            `json:"video,omitempty"`
	LiveSession      *LiveSessionNode      `json:"live_session,omitempty"`
	Batch            *BatchNode            `json:"batch,omitempty"`
	MCPServer        *MCPServerNode        `json:"mcp_server,omitempty"`
	DataRetention    *DataRetentionNode    `json:"data_retention,omitempty"`
}

// CapabilityEdgeType 是 capability graph 的 5 种边类型（按 P-0 综合 spec §2.4）。
//
//   - provides：node A 提供 node B 所需能力（如 mcp_server provides tool_use）
//   - requires：node A 依赖 node B（如 tool_result requires tool_use）
//   - mutually_exclusive：A 与 B 不可共存（projection 只能选一）
//   - loses：投影时 lossy 关系（在某 vendor 下 A 退化为 B）
//   - requires_native：必须走 native passthrough 才能保留
type CapabilityEdgeType string

const (
	EdgeProvides          CapabilityEdgeType = "provides"
	EdgeRequires          CapabilityEdgeType = "requires"
	EdgeMutuallyExclusive CapabilityEdgeType = "mutually_exclusive"
	EdgeLoses             CapabilityEdgeType = "loses"
	EdgeRequiresNative    CapabilityEdgeType = "requires_native"
)

// AllEdgeTypes 列出所有合法 edge type，envelope_validate 与测试遍历用。
var AllEdgeTypes = []CapabilityEdgeType{
	EdgeProvides,
	EdgeRequires,
	EdgeMutuallyExclusive,
	EdgeLoses,
	EdgeRequiresNative,
}

// CapabilityEdge 是 capability graph 的有向边；From 指向 To。
type CapabilityEdge struct {
	// ID 必填；envelope 内唯一。
	ID string `json:"id"`

	// Type 必填；5 种边类型之一。
	Type CapabilityEdgeType `json:"type"`

	// From 必填；起点 node ID。
	From string `json:"from"`

	// To 必填；终点 node ID。
	To string `json:"to"`

	// Required 必填；边是否为必须项；true 表示 projection 不能丢这个边。
	Required bool `json:"required"`

	// Reason 可选；边的人读说明。
	Reason string `json:"reason,omitempty"`

	// ProtocolLoss 可选；边自身的 loss（如 EdgeLoses 时记录退化原因）。
	ProtocolLoss []ProtocolLossEntry `json:"protocol_loss,omitempty"`
}
