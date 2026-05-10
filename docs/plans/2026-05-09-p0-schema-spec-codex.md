# P-0 Schema Spec — HCSFEnvelope v0.4 Go Type 锁定 (Codex Lane)

**日期**: 2026-05-09
**Lane**: codex (xhigh + fast_mode)
**对应 Claude lane**: docs/plans/2026-05-09-p0-schema-spec-claude.md（写作时未见）
**范围**: HUAKAI 内部 schema/spec 草案；不写实现代码；不读 reference source；不做 schema migration。

## TL;DR

P-0b 应新增 `proto.HCSFEnvelope`，并把当前空壳 `proto.HCSF` 改成 `type HCSF = HCSFEnvelope`，这样现有 `ClientAdapter` / `UpstreamAdapter` 的 `*HCSF` 签名可以继续编译，同时新代码开始使用 `*HCSFEnvelope` 命名。依据：当前 `proto.HCSF` 是空 struct，adapter 接口已经以 `*HCSF` 为边界；现有 `CanonicalRequest` / `CanonicalMessage` / `CanonicalEvent` / `CanonicalResponse` / `CanonicalUsage` 已经存在但没有顶层 wrapper（`backend/internal/proto/proto.go:13-34`, `backend/internal/proto/hcsf.go:11-119`, `docs/research/2026-05-09-axis3-huakai-current-state.md:124-138`）。

计数口径锁定为 **14 个 capability families**，不再合并为 `multimodal`。为让 tool 链路可寻址，Go schema 提供 15 个 concrete node payload：`tool_use` 和 `tool_result` 是两个 node kind，但属于同一个已批 tool capability family；`data_retention` 计入 14 families，不再另算第 15 个 product capability。依据：综合计划将 `tool_use / tool_result` 作为一个 capability 行，同时把 `data_retention` 列为第 14 项；Owner 已批 D2=14 capability 和 D12=data_retention 五词汇（`docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:42-64`, `docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:161-164`）。

`protocol_loss` 必须升级为 envelope / capability / projection 三层一等公民。P-0b 不删除旧字段 `feature/direction/verdict/note`，而是在现有 `ProtocolLossEntry` 上追加 `field/vendor/severity/reason/suggestion` 等新字段；旧适配器继续可用，新 projection 必须填新字段。依据：已发布 F-PROTO-002 要求 capability matrix、Usage Record `protocol_loss`、lossy/unsupported 显性化；当前代码已经有较窄的 `ProtocolLossEntry`（`docs/specs/protocol-translation.md:87-124`, `docs/specs/protocol-translation.md:138-151`, `backend/internal/proto/proto.go:36-61`）。

P-0b 不入库，不改 billing ledger / auth core / quota enforcement / DB schema。HCSF v0.4 仅是内存 IR，native passthrough 采用 standard auth + audit 的 schema 表达，不在本阶段实现路由。依据：Owner 已批 D1/D3 不入库，D5 standard auth + audit，且合成计划把 P-0 输出限定为 Go type/JSON 字段锁定（`docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:127-145`）。

## 1. HCSFEnvelope v0.4 顶层结构 (Go + JSON)

### 1.1 顶层锁定

P-0b 在 `backend/internal/proto` 内新增以下类型。代码注释必须中文，标识符与 JSON tag 保持英文；本仓既有计划已记录“注释中文、标识符英文”的 Owner 约束（`docs/plans/2026-05-08-pasr-lite-v2-codex.md:154`, `docs/plans/2026-05-09-next-pivot-claude.md:228`）。我没有在仓库中定位到独立的 `feedback_chinese_comments.md` 文件，风险见末尾。

```go
type HCSFEnvelope struct {
	// Version 必填；默认 "0.4"；P-0b 构造器必须显式写入，测试禁止空版本。
	Version string `json:"version"`

	// RequestMeta 必填；记录入口协议、路由身份、上游协议族和 request 级审计锚点。
	RequestMeta RequestMeta `json:"request_meta"`

	// RequestControls 必填；承接现有 CanonicalRequest 中除 model/messages/stream 外的请求控制字段。
	RequestControls RequestControls `json:"request_controls"`

	// Messages 必填；可为空数组；复用现有 CanonicalMessage，保留当前 adapter 兼容面。
	Messages []CanonicalMessage `json:"messages"`

	// BufferedResponse 可选；用于 non-streaming ProviderResponseToCanonical，不再返回空 HCSF。
	BufferedResponse *CanonicalResponse `json:"buffered_response,omitempty"`

	// StreamEvents 可选；用于测试和 buffered replay，不作为 forwarder hot path 的强制缓存。
	StreamEvents []CanonicalEvent `json:"stream_events,omitempty"`

	// CapabilityGraph 必填；承载 14 capability families 的 node/edge/loss 表达。
	CapabilityGraph CapabilityGraph `json:"capability_graph"`

	// ProviderProjection 必填；记录当前目标 vendor/protocol 对每个 capability 的投影结果。
	ProviderProjection ProviderProjection `json:"provider_projection"`

	// StreamPlan 必填；stream=false 时 Mode="buffered"；D9 中段 fallback 只留 schema hook。
	StreamPlan StreamPlan `json:"stream_plan"`

	// Accounting 必填；承接 CanonicalUsage、cache/reasoning/live/batch usage 和证据标签。
	Accounting Accounting `json:"accounting"`

	// Policy 必填；承接 data_retention、native passthrough auth/audit、redaction。
	Policy Policy `json:"policy"`

	// Extensions 可选；仅给已记录的推迟决策点或 vendor 临时 metadata 使用；不得用于隐藏 capability drop。
	Extensions map[string]json.RawMessage `json:"extensions,omitempty"`
}
```

字段来源与约束：`Messages` 复用 `CanonicalMessage`，`BufferedResponse` 补当前 OpenAI/Gemini buffered response 无 envelope slot 的缺口，`StreamEvents` 复用 `CanonicalEvent`，`Accounting` 复用 `CanonicalUsage`；这些类型均已存在于 `hcsf.go`（`backend/internal/proto/hcsf.go:36-107`）。`RequestControls` 显式承接当前 `CanonicalRequest` 中 tools/tool_choice/max_tokens/stop/temperature/top_p/system_prompt/parallel_tool_calls/response_format 字段，避免 envelope 只保留 messages 后丢请求控制项（`backend/internal/proto/hcsf.go:11-27`）。现有 OpenAI/Gemini non-streaming path 返回 `&HCSF{}` 并附加 lossy entry，原因就是当前 envelope 没有 buffered response slot（`backend/internal/proto/openai_sse.go:148-156`, `backend/internal/proto/gemini_sse.go:103-111`）。

### 1.2 RequestMeta

```go
type RequestMeta struct {
	// RequestID 必填；HUAKAI 内部 request 追踪 ID；没有上游 ID 时由入口生成。
	RequestID string `json:"request_id"`

	// TenantID 可选；0 表示无租户上下文；P-0b 不改变 auth/tenant 解析逻辑。
	TenantID int64 `json:"tenant_id,omitempty"`

	// RouteID 可选；沿用 gateway ForwardRequest.RouteID 的语义。
	RouteID string `json:"route_id,omitempty"`

	// AccountID 可选；选中 provider_account_id；仅用于审计/usage/PASR 反馈关联。
	AccountID int64 `json:"account_id,omitempty"`

	// AcquisitionToken 可选；字符串化 UUID；避免 proto 包新增 uuid runtime dependency。
	AcquisitionToken string `json:"acquisition_token,omitempty"`

	// ClientProtocol 必填；合法值沿用 openai_chat/openai_responses/anthropic_messages。
	ClientProtocol ClientProtocol `json:"client_protocol"`

	// ProtocolFamily 必填；forwarder/dispatcher 当前用它选择 upstream adapter/provider adapter。
	ProtocolFamily string `json:"protocol_family"`

	// UpstreamProtocol 可选；沿用 CapabilityMatrix 的 upstream enum，用于 matrix/projection。
	UpstreamProtocol UpstreamProtocol `json:"upstream_protocol,omitempty"`

	// Provider 可选；人读 vendor 名，如 "anthropic" / "openai" / "gemini" / "bedrock"。
	Provider string `json:"provider,omitempty"`

	// Model 必填；入口模型名。
	Model string `json:"model"`

	// UpstreamModel 可选；registry 解析后的真实上游模型名。
	UpstreamModel string `json:"upstream_model,omitempty"`

	// IngressPath 必填；如 "/v1/chat/completions"、"/v1/messages"、"/v1/native/openai/responses"。
	IngressPath string `json:"ingress_path"`

	// IdempotencyKey 可选；保留未来 batch/live/retry 幂等钩子。
	IdempotencyKey string `json:"idempotency_key,omitempty"`

	// SessionHash 可选；当前用于 PASR prefix feedback；空值表示不触发 prefix segment 更新。
	SessionHash string `json:"session_hash,omitempty"`

	// NativePassthrough 必填；默认 false；true 表示本 envelope 是 native route 的审计/投影壳。
	NativePassthrough bool `json:"native_passthrough"`

	// EvidenceLabel 可选；默认 "mock"；P-6 后才可写 "smoke" 或 "real"。
	EvidenceLabel EvidenceLabel `json:"evidence_label,omitempty"`
}

type EvidenceLabel string

const (
	EvidenceMock  EvidenceLabel = "mock"
	EvidenceSmoke EvidenceLabel = "smoke"
	EvidenceReal  EvidenceLabel = "real"
)
```

`RequestMeta` 与当前 forwarder/dispatcher 的关系是映射而非替换：`ForwardRequest` 已有 TenantID、AccountID、AcquisitionToken、RouteID、ProtocolFamily、UpstreamProtocol、ClientProtocol、Model、RoutingReasonPayload、SessionHash；dispatcher 用 ProtocolFamily 选择 provider adapter 并把 body 交给上游（`backend/internal/gateway/forwarder_types.go:95-127`, `backend/internal/gateway/upstream_dispatcher.go:37-52`, `backend/internal/gateway/upstream_dispatcher.go:102-149`）。当前 forwarder 对空 ProtocolFamily fail-loud，P-0b 不改变该行为（`backend/internal/gateway/forwarder.go:79-94`）。

### 1.3 RequestControls

```go
type RequestControls struct {
	// Tools 可选；复用 CanonicalTool；空数组表示无工具声明。
	Tools []CanonicalTool `json:"tools,omitempty"`

	// ToolChoice 可选；保留原始 canonical JSON，避免 P-0b 先决定各 vendor tool_choice dialect。
	ToolChoice json.RawMessage `json:"tool_choice,omitempty"`

	// MaxTokens 可选；0 表示未指定。
	MaxTokens int `json:"max_tokens,omitempty"`

	// StopSequences 可选；空数组表示未指定。
	StopSequences []string `json:"stop_sequences,omitempty"`

	// Temperature 可选；nil 表示未指定，不能与 0.0 混淆。
	Temperature *float64 `json:"temperature,omitempty"`

	// TopP 可选；nil 表示未指定，不能与 0.0 混淆。
	TopP *float64 `json:"top_p,omitempty"`

	// SystemPrompt 可选；空串表示未指定；系统内容的 cache sanitizer 由 cache_control node 表达。
	SystemPrompt string `json:"system_prompt,omitempty"`

	// ParallelToolCalls 可选；默认 false。
	ParallelToolCalls bool `json:"parallel_tool_calls,omitempty"`

	// ResponseFormat 可选；用 RawMessage 保留 OpenAI/Gemini/Anthropic schema dialect。
	ResponseFormat json.RawMessage `json:"response_format,omitempty"`
}
```

这些字段来自现有 `CanonicalRequest`，但 `Stream` 移入 `StreamPlan`，`Model` 移入 `RequestMeta`，`Messages` 保持顶层一等字段（`backend/internal/proto/hcsf.go:11-27`）。`ToolChoice` 与 `ResponseFormat` 用 `json.RawMessage` 是 P-0b 的保守选择，因为现状 audit 明确指出 tool_choice 跨 vendor 互转代码为 0，structured output dialect 需要后续 provider projection 决定（`docs/research/2026-05-09-axis3-huakai-current-state.md:162-167`, `docs/research/2026-05-09-issue-mining-cross-repo.md:243-248`）。

## 2. 14 Capability Nodes

### 2.1 计数口径

D2 的产品口径是 14 capability families：`text`、`tool`（包含 `tool_use` + `tool_result` 两个 concrete node kind）、`thinking`、`cache_control`、`structured_output`、`computer_use`、`file`、`image`、`audio`、`video`、`live_session`、`batch`、`mcp_server`、`data_retention`。不新增 `multimodal` 聚合节点，因为 issue mining 和合成计划都把 file/image/audio/video 的断点与生命周期分开处理（`docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:40-64`, `docs/plans/2026-05-09-hcsf-v04-implementation-codex.md:24-41`, `docs/research/2026-05-09-issue-mining-cross-repo.md:78-95`, `docs/research/2026-05-09-issue-mining-cross-repo.md:108-120`）。

### 2.2 Node wrapper 与枚举

```go
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

type StreamReadiness string

const (
	StreamReadyYes     StreamReadiness = "yes"
	StreamReadyNo      StreamReadiness = "no"
	StreamReadyPartial StreamReadiness = "partial"
)

type CapabilityGraph struct {
	// Nodes 必填；可为空数组；empty graph 是合法 fixture。
	Nodes []CapabilityNode `json:"nodes"`

	// Edges 必填；可为空数组；node 间因果、依赖、usage、policy 关系写这里。
	Edges []CapabilityEdge `json:"edges"`

	// ProtocolLoss 可选；图级 loss，node/projection 更具体时优先写到更具体位置。
	ProtocolLoss []ProtocolLossEntry `json:"protocol_loss,omitempty"`
}

type CapabilityNode struct {
	// ID 必填；envelope 内唯一；建议格式 "n_<kind>_<seq>"，不含 vendor 前缀。
	ID string `json:"id"`

	// Kind 必填；必须与下方恰好一个 payload 字段对应。
	Kind CapabilityKind `json:"kind"`

	// Source 可选；指向 messages/events/request field 的来源位置。
	Source NodeSourceRef `json:"source,omitempty"`

	// StreamReady 必填；取 yes/no/partial；用于 matrix 与 StreamPlan 校验。
	StreamReady StreamReadiness `json:"stream_ready"`

	// ProtocolLoss 可选；该 capability 自身产生的 loss。
	ProtocolLoss []ProtocolLossEntry `json:"protocol_loss,omitempty"`

	// Text 可选；Kind=text 时必填，其他 Kind 必须为空。
	Text *TextNode `json:"text,omitempty"`

	// ToolUse 可选；Kind=tool_use 时必填，其他 Kind 必须为空。
	ToolUse *ToolUseNode `json:"tool_use,omitempty"`

	// ToolResult 可选；Kind=tool_result 时必填，其他 Kind 必须为空。
	ToolResult *ToolResultNode `json:"tool_result,omitempty"`

	// Thinking 可选；Kind=thinking 时必填，其他 Kind 必须为空。
	Thinking *ThinkingNode `json:"thinking,omitempty"`

	// CacheControl 可选；Kind=cache_control 时必填，其他 Kind 必须为空。
	CacheControl *CacheControlNode `json:"cache_control,omitempty"`

	// StructuredOutput 可选；Kind=structured_output 时必填，其他 Kind 必须为空。
	StructuredOutput *StructuredOutputNode `json:"structured_output,omitempty"`

	// ComputerUse 可选；Kind=computer_use 时必填，其他 Kind 必须为空。
	ComputerUse *ComputerUseNode `json:"computer_use,omitempty"`

	// File 可选；Kind=file 时必填，其他 Kind 必须为空。
	File *FileNode `json:"file,omitempty"`

	// Image 可选；Kind=image 时必填，其他 Kind 必须为空。
	Image *ImageNode `json:"image,omitempty"`

	// Audio 可选；Kind=audio 时必填，其他 Kind 必须为空。
	Audio *AudioNode `json:"audio,omitempty"`

	// Video 可选；Kind=video 时必填，其他 Kind 必须为空。
	Video *VideoNode `json:"video,omitempty"`

	// LiveSession 可选；Kind=live_session 时必填，其他 Kind 必须为空。
	LiveSession *LiveSessionNode `json:"live_session,omitempty"`

	// Batch 可选；Kind=batch 时必填，其他 Kind 必须为空。
	Batch *BatchNode `json:"batch,omitempty"`

	// MCPServer 可选；Kind=mcp_server 时必填，其他 Kind 必须为空。
	MCPServer *MCPServerNode `json:"mcp_server,omitempty"`

	// DataRetention 可选；Kind=data_retention 时必填，其他 Kind 必须为空。
	DataRetention *DataRetentionNode `json:"data_retention,omitempty"`
}

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
```

Wrapper 采用 tagged-union 指针字段，是为了让 P-0b 能用标准 `encoding/json` 做稳定 round-trip，不引入 runtime dependency，也不需要 interface 自定义 marshal。现有 CapabilityMatrix 已是内存 map，不是 typed union；P-0b 需要更细 node payload 来支撑 per-capability fixture 与 projection（`backend/internal/proto/capability_matrix.go:59-110`, `docs/plans/2026-05-09-hcsf-v04-implementation-codex.md:253-257`）。

### 2.3 Capability table

| Family count | Concrete Go type | JSON tag | 类型形态 | Stream? | 必填字段 |
|---:|---|---|---|---|---|
| 1 | `TextNode` | `text` | 复用 `CanonicalContentBlock` | yes | `role`, `block` |
| 2a | `ToolUseNode` | `tool_use` | `CanonicalContentBlock` + edge | yes | `tool_call_id`, `name`, `input` |
| 2b | `ToolResultNode` | `tool_result` | `CanonicalContentBlock` + edge | yes | `tool_call_id`, `content`, `status` |
| 3 | `ThinkingNode` | `thinking` | ContentBlock | yes | `budget_tokens`, `redaction`, `blocks` |
| 4 | `CacheControlNode` | `cache_control` | meta-node | no | `scope`, `breakpoint_refs`, `sanitize_system_metadata` |
| 5 | `StructuredOutputNode` | `structured_output` | request-node | yes | `mode`, `strict`, `schema` |
| 6 | `ComputerUseNode` | `computer_use` | hosted-tool node | yes | `environment`, `action`, `approval` |
| 7 | `FileNode` | `file` | content-node | no | `source_kind`, `media_type`, `locator` |
| 8 | `ImageNode` | `image` | content-node | no | `source_kind`, `media_type`, `locator` |
| 9 | `AudioNode` | `audio` | content-node | partial | `transport`, `format`, `locator` |
| 10 | `VideoNode` | `video` | content-node | no | `source_kind`, `media_type`, `locator` |
| 11 | `LiveSessionNode` | `live_session` | session-graph | yes (WSS) | `session_id`, `transport`, `modalities` |
| 12 | `BatchNode` | `batch` | job-graph | no | `job_id`, `endpoint`, `input_ref` |
| 13 | `MCPServerNode` | `mcp_server` | external-capability | yes | `server_label`, `allowed_operations`, `approval_required` |
| 14 | `DataRetentionNode` | `data_retention` | policy-assertion | no | `value`, `enforcement`, `audit_label` |

ToolUse/ToolResult 拆 concrete node 是为了表达 `tool_use -> tool_result` edge；产品计数仍按一个 tool family。现状 audit 已指出 tool_result block 已定义但没有任何 adapter emit，tool_choice/tool_result 是跨 vendor invariant 缺口（`backend/internal/proto/hcsf.go:42-52`, `docs/research/2026-05-09-axis3-huakai-current-state.md:154-167`, `docs/research/2026-05-09-axis3-huakai-current-state.md:310-320`）。

### 2.4 Node payload structs

```go
type TextNode struct {
	// Role 必填；取 user/assistant/system/tool 等 canonical role。
	Role string `json:"role"`

	// Block 必填；Block.Type 必须为 "text"。
	Block CanonicalContentBlock `json:"block"`
}

type ToolUseNode struct {
	// ToolCallID 必填；canonical id；D4 未决前不固定 SHA-8/SHA-12 长度。
	ToolCallID string `json:"tool_call_id"`

	// OriginalToolCallID 可选；保留上游原始 id，禁止泄露到不兼容客户端。
	OriginalToolCallID string `json:"original_tool_call_id,omitempty"`

	// Name 必填；规范化 display/tool name。
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

type ToolResultNode struct {
	// ToolCallID 必填；必须通过 edge 指向 ToolUseNode。
	ToolCallID string `json:"tool_call_id"`

	// Content 必填；可为空数组；复用 CanonicalContentBlock。
	Content []CanonicalContentBlock `json:"content"`

	// Status 必填；complete/error。
	Status ToolNodeStatus `json:"status"`

	// IsError 必填；默认 false。
	IsError bool `json:"is_error"`
}

type ToolNodeStatus string

const (
	ToolNodePending  ToolNodeStatus = "pending"
	ToolNodePartial  ToolNodeStatus = "partial"
	ToolNodeComplete ToolNodeStatus = "complete"
	ToolNodeError    ToolNodeStatus = "error"
)

type ThinkingNode struct {
	// BudgetTokens 必填；0 表示 provider 未声明 budget。
	BudgetTokens int `json:"budget_tokens"`

	// Blocks 必填；可为空数组；visible thinking/reasoning 内容用 CanonicalContentBlock 表达。
	Blocks []CanonicalContentBlock `json:"blocks"`

	// HiddenTokens 可选；provider 报告但不可见的 reasoning token。
	HiddenTokens int `json:"hidden_tokens,omitempty"`

	// Signature 可选；Anthropic signature_delta 等策略受控字段。
	Signature string `json:"signature,omitempty"`

	// Redaction 必填；public/redacted/hidden/provider_only。
	Redaction RedactionClass `json:"redaction"`
}

type RedactionClass string

const (
	RedactionPublic       RedactionClass = "public"
	RedactionRedacted     RedactionClass = "redacted"
	RedactionHidden       RedactionClass = "hidden"
	RedactionProviderOnly RedactionClass = "provider_only"
)

type CacheControlNode struct {
	// Scope 必填；request/message/block/session/vendor。
	Scope CacheScope `json:"scope"`

	// BreakpointRefs 必填；指向 node id 或 message/block ref；空数组表示无断点但保留 cache policy。
	BreakpointRefs []string `json:"breakpoint_refs"`

	// CacheKeyHint 可选；只能是 hash/hint，禁止写 prompt 明文。
	CacheKeyHint string `json:"cache_key_hint,omitempty"`

	// CacheCreationInputTokens 可选；映射 CanonicalUsage.CacheCreationInputTokens。
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`

	// CacheReadInputTokens 可选；映射 CanonicalUsage.CacheReadInputTokens。
	CacheReadInputTokens int `json:"cache_read_input_tokens,omitempty"`

	// SanitizeSystemMetadata 必填；默认 true；防止动态 billing/header metadata 破坏 prefix cache。
	SanitizeSystemMetadata bool `json:"sanitize_system_metadata"`
}

type CacheScope string

const (
	CacheScopeRequest CacheScope = "request"
	CacheScopeMessage CacheScope = "message"
	CacheScopeBlock   CacheScope = "block"
	CacheScopeSession CacheScope = "session"
	CacheScopeVendor  CacheScope = "vendor"
)

type StructuredOutputNode struct {
	// Mode 必填；json_mode/json_schema/tool_strategy/provider_native。
	Mode StructuredOutputMode `json:"mode"`

	// Strict 必填；默认 false。
	Strict bool `json:"strict"`

	// Schema 必填；无 schema 时写 JSON null，不省略。
	Schema json.RawMessage `json:"schema"`

	// ParserMode 可选；client/provider/parser 的约束来源。
	ParserMode string `json:"parser_mode,omitempty"`

	// FailureRecovery 可选；none/retry/repair/native_required。
	FailureRecovery string `json:"failure_recovery,omitempty"`

	// FallbackStrategy 可选；prompt/tool/native/unsupported。
	FallbackStrategy string `json:"fallback_strategy,omitempty"`
}

type StructuredOutputMode string

const (
	StructuredOutputJSONMode        StructuredOutputMode = "json_mode"
	StructuredOutputJSONSchema      StructuredOutputMode = "json_schema"
	StructuredOutputToolStrategy    StructuredOutputMode = "tool_strategy"
	StructuredOutputProviderNative  StructuredOutputMode = "provider_native"
)

type ComputerUseNode struct {
	// Environment 必填；browser/desktop/shell/mobile/other。
	Environment string `json:"environment"`

	// Action 必填；provider-native action 名或 HUAKAI normalized action。
	Action string `json:"action"`

	// Input 可选；action 参数 JSON。
	Input json.RawMessage `json:"input,omitempty"`

	// ScreenshotRef 可选；指向 image/file node id。
	ScreenshotRef string `json:"screenshot_ref,omitempty"`

	// Approval 必填；required/granted/denied/not_required。
	Approval ApprovalState `json:"approval"`

	// AuditLabel 可选；native/computer-use 操作审计标签。
	AuditLabel string `json:"audit_label,omitempty"`
}

type ApprovalState string

const (
	ApprovalRequired    ApprovalState = "required"
	ApprovalGranted     ApprovalState = "granted"
	ApprovalDenied      ApprovalState = "denied"
	ApprovalNotRequired ApprovalState = "not_required"
)

type FileNode struct {
	// SourceKind 必填；inline_base64/url/file_id/digest_ref。
	SourceKind DataSourceKind `json:"source_kind"`

	// MediaType 必填；MIME type，如 application/pdf。
	MediaType string `json:"media_type"`

	// Locator 必填；不要求 P-0b 解析 provider file lifecycle。
	Locator DataLocator `json:"locator"`

	// SizeBytes 可选；0 表示未知。
	SizeBytes int64 `json:"size_bytes,omitempty"`

	// Digest 可选；内容 hash 或外部 digest ref，禁止写明文 secret。
	Digest string `json:"digest,omitempty"`

	// Retention 可选；与 DataRetentionNode.Value 关联的人读标签。
	Retention string `json:"retention,omitempty"`
}

type ImageNode struct {
	// SourceKind 必填；inline_base64/url/file_id/digest_ref。
	SourceKind DataSourceKind `json:"source_kind"`

	// MediaType 必填；MIME type，如 image/png。
	MediaType string `json:"media_type"`

	// Locator 必填；图片数据位置或 provider file id。
	Locator DataLocator `json:"locator"`

	// Dimensions 可选；未知时省略。
	Dimensions MediaDimensions `json:"dimensions,omitempty"`
}

type AudioNode struct {
	// Transport 必填；inline/file/url/stream。
	Transport MediaTransport `json:"transport"`

	// Format 必填；如 wav/mp3/opus/pcm16。
	Format string `json:"format"`

	// Locator 必填；音频数据位置或 stream ref。
	Locator DataLocator `json:"locator"`

	// SampleRateHz 可选；0 表示未知。
	SampleRateHz int `json:"sample_rate_hz,omitempty"`

	// Channels 可选；0 表示未知。
	Channels int `json:"channels,omitempty"`

	// DurationMillis 可选；0 表示未知。
	DurationMillis int64 `json:"duration_ms,omitempty"`

	// TranscriptPolicy 可选；none/requested/provided。
	TranscriptPolicy TranscriptPolicy `json:"transcript_policy,omitempty"`

	// LiveCompatible 必填；默认 false；true 表示可连接 live_session。
	LiveCompatible bool `json:"live_compatible"`
}

type VideoNode struct {
	// SourceKind 必填；inline_base64/url/file_id/digest_ref。
	SourceKind DataSourceKind `json:"source_kind"`

	// MediaType 必填；MIME type，如 video/mp4。
	MediaType string `json:"media_type"`

	// Locator 必填；视频数据位置或 provider file id。
	Locator DataLocator `json:"locator"`

	// Dimensions 可选；未知时省略。
	Dimensions MediaDimensions `json:"dimensions,omitempty"`

	// TimeRange 可选；剪辑范围，单位毫秒。
	TimeRange TimeRange `json:"time_range,omitempty"`

	// Codec 可选；如 h264/vp9/av1。
	Codec string `json:"codec,omitempty"`

	// SizeBytes 可选；0 表示未知。
	SizeBytes int64 `json:"size_bytes,omitempty"`
}

type DataSourceKind string

const (
	DataSourceInlineBase64 DataSourceKind = "inline_base64"
	DataSourceURL          DataSourceKind = "url"
	DataSourceFileID       DataSourceKind = "file_id"
	DataSourceDigestRef    DataSourceKind = "digest_ref"
)

type MediaTransport string

const (
	MediaTransportInline MediaTransport = "inline"
	MediaTransportFile   MediaTransport = "file"
	MediaTransportURL    MediaTransport = "url"
	MediaTransportStream MediaTransport = "stream"
)

type DataLocator struct {
	// Kind 必填；inline_base64/url/file_id/digest_ref。
	Kind DataSourceKind `json:"kind"`

	// Value 必填；base64 本体、URL、provider file id 或 digest ref。
	Value string `json:"value"`
}

type MediaDimensions struct {
	// Width 可选；0 表示未知。
	Width int `json:"width,omitempty"`

	// Height 可选；0 表示未知。
	Height int `json:"height,omitempty"`
}

type TimeRange struct {
	// StartMillis 可选；默认 0。
	StartMillis int64 `json:"start_ms,omitempty"`

	// EndMillis 可选；0 表示到媒体结尾或未知。
	EndMillis int64 `json:"end_ms,omitempty"`
}

type TranscriptPolicy string

const (
	TranscriptNone      TranscriptPolicy = "none"
	TranscriptRequested TranscriptPolicy = "requested"
	TranscriptProvided  TranscriptPolicy = "provided"
)

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

type LiveTransport string

const (
	LiveTransportWSS LiveTransport = "wss"
	LiveTransportSSE LiveTransport = "sse"
)

type BatchNode struct {
	// JobID 必填；HUAKAI 或 provider batch job id。
	JobID string `json:"job_id"`

	// Endpoint 必填；batch 目标端点或 native capability。
	Endpoint string `json:"endpoint"`

	// InputRef 必填；指向 file node id、URL 或 provider input file id。
	InputRef string `json:"input_ref"`

	// Validation 必填；pending/validated/failed/complete。
	Validation BatchStatus `json:"validation"`

	// OutputRef 可选；输出文件或结果引用。
	OutputRef string `json:"output_ref,omitempty"`

	// ErrorRef 可选；错误文件或错误结果引用。
	ErrorRef string `json:"error_ref,omitempty"`

	// RetryPolicy 可选；batch/job retry policy。
	RetryPolicy RetryPolicy `json:"retry_policy,omitempty"`

	// CostAttribution 可选；成本归因标签或 account ref。
	CostAttribution string `json:"cost_attribution,omitempty"`
}

type BatchStatus string

const (
	BatchPending   BatchStatus = "pending"
	BatchValidated BatchStatus = "validated"
	BatchFailed    BatchStatus = "failed"
	BatchComplete  BatchStatus = "complete"
)

type RetryPolicy struct {
	// MaxAttempts 可选；0 表示未指定。
	MaxAttempts int    `json:"max_attempts,omitempty"`

	// Backoff 可选；如 fixed/exponential/provider_default。
	Backoff string `json:"backoff,omitempty"`
}

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

type DataRetentionNode struct {
	// Value 必填；只能取 D12 五词汇。
	Value DataRetentionValue `json:"value"`

	// Enforcement 必填；unknown/asserted/contract_required/verified。
	Enforcement string `json:"enforcement"`

	// Region 可选；regional_asserted 时填写区域。
	Region string `json:"region,omitempty"`

	// RequestStore 可选；request_store_false 时必须为 false。
	RequestStore *bool `json:"request_store,omitempty"`

	// NoTrain 可选；表达 no-train intent，不等同 ZDR proof。
	NoTrain bool `json:"no_train,omitempty"`

	// EvidenceRef 可选；zdr_verified 必须填 Owner 提供的 account/vendor proof ref。
	EvidenceRef string `json:"evidence_ref,omitempty"`

	// AuditLabel 必填；用于 native/policy audit 查询。
	AuditLabel string `json:"audit_label"`
}

type DataRetentionValue string

const (
	DataRetentionUnknown                  DataRetentionValue = "unknown"
	DataRetentionRequestStoreFalse        DataRetentionValue = "request_store_false"
	DataRetentionProviderContractRequired DataRetentionValue = "provider_contract_required"
	DataRetentionRegionalAsserted         DataRetentionValue = "regional_asserted"
	DataRetentionZDRVerified              DataRetentionValue = "zdr_verified"
)
```

`CacheControlNode` 不包含跨账号复制字段，这是 D6 已批的保守边界；schema 只保留当前 request/message/block/session/vendor scope 和 sanitizer 钩子。跨账号 cache 复制属于 P-8 roadmap，不在 P-0b 类型中出现，避免 executor 误以为已经获得 Direction 1 授权（`docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:146-149`, `docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:36-37`）。cache sanitizer 是针对 new-api#4678 动态 metadata 破坏 prefix cache 的 issue-derived需求（`docs/research/2026-05-09-issue-mining-cross-repo.md:112-115`, `docs/research/2026-05-09-issue-mining-cross-repo.md:243-245`）。

## 3. CapabilityGraph 边模型

```go
type CapabilityEdgeType string

const (
	EdgeMessageToToolUse        CapabilityEdgeType = "message_to_tool_use"
	EdgeToolUseToToolResult     CapabilityEdgeType = "tool_use_to_tool_result"
	EdgeResponseToUsage         CapabilityEdgeType = "response_to_usage"
	EdgeFileToModalityInput     CapabilityEdgeType = "file_to_modality_input"
	EdgeMCPServerToToolCall     CapabilityEdgeType = "mcp_server_to_tool_call"
	EdgeCapabilityDependency    CapabilityEdgeType = "capability_dependency"
	EdgePolicyAppliesToNode     CapabilityEdgeType = "policy_applies_to_node"
	EdgeNativePassthroughTarget CapabilityEdgeType = "native_passthrough_target"
)

type CapabilityEdge struct {
	ID           string              `json:"id"`
	Type         CapabilityEdgeType  `json:"type"`
	From         EdgeEndpoint        `json:"from"`
	To           EdgeEndpoint        `json:"to"`
	Required     bool                `json:"required"`
	ProtocolLoss []ProtocolLossEntry `json:"protocol_loss,omitempty"`

	MessageToToolUse        *MessageToToolUseEdge        `json:"message_to_tool_use,omitempty"`
	ToolUseToToolResult     *ToolUseToToolResultEdge     `json:"tool_use_to_tool_result,omitempty"`
	ResponseToUsage         *ResponseToUsageEdge         `json:"response_to_usage,omitempty"`
	FileToModalityInput     *FileToModalityInputEdge     `json:"file_to_modality_input,omitempty"`
	MCPServerToToolCall     *MCPServerToToolCallEdge     `json:"mcp_server_to_tool_call,omitempty"`
	CapabilityDependency    *CapabilityDependencyEdge    `json:"capability_dependency,omitempty"`
	PolicyAppliesToNode     *PolicyAppliesToNodeEdge     `json:"policy_applies_to_node,omitempty"`
	NativePassthroughTarget *NativePassthroughTargetEdge `json:"native_passthrough_target,omitempty"`
}

type EdgeEndpoint struct {
	NodeID       string `json:"node_id,omitempty"`
	MessageIndex *int  `json:"message_index,omitempty"`
	BlockIndex  *int   `json:"block_index,omitempty"`
	Field       string `json:"field,omitempty"`
}

type MessageToToolUseEdge struct {
	MessageIndex int    `json:"message_index"`
	ToolNodeID   string `json:"tool_node_id"`
	ToolCallID   string `json:"tool_call_id"`
}

type ToolUseToToolResultEdge struct {
	ToolUseNodeID    string `json:"tool_use_node_id"`
	ToolResultNodeID string `json:"tool_result_node_id"`
	ToolCallID       string `json:"tool_call_id"`
}

type ResponseToUsageEdge struct {
	ResponseID string `json:"response_id"`
	UsageField string `json:"usage_field"`
}

type FileToModalityInputEdge struct {
	FileNodeID     string         `json:"file_node_id"`
	ModalityNodeID string        `json:"modality_node_id"`
	Modality       CapabilityKind `json:"modality"`
}

type MCPServerToToolCallEdge struct {
	ServerNodeID string `json:"server_node_id"`
	ToolNodeID   string `json:"tool_node_id"`
	Operation    string `json:"operation"`
}

type CapabilityDependencyEdge struct {
	DependencyNodeID string `json:"dependency_node_id"`
	DependentNodeID  string `json:"dependent_node_id"`
	Reason           string `json:"reason"`
}

type PolicyAppliesToNodeEdge struct {
	PolicyNodeID string `json:"policy_node_id"`
	TargetNodeID string `json:"target_node_id"`
	PolicyField  string `json:"policy_field"`
}

type NativePassthroughTargetEdge struct {
	NodeID     string `json:"node_id"`
	Vendor    string `json:"vendor"`
	NativePath string `json:"native_path"`
}
```

这组 edge 覆盖 user 指定的 `message -> tool_use`、`tool_use -> tool_result`、`response -> usage`、`file -> modality input`、`mcp_server -> tool_call`，并额外保留 policy/native/dependency 三类通用边。Tool edge 是必须项，因为当前 tool ID 转换已有代码，但跨 vendor partial JSON reassembly、tool_choice、tool_result 仍缺 invariant 测试；graph edge 可以让 fixture 精确断言 use/result 配对（`backend/internal/proto/tool_call_id.go`, `docs/research/2026-05-09-axis3-huakai-current-state.md:154-167`, `docs/research/2026-05-09-axis3-huakai-current-state.md:310-320`）。`response_to_usage` edge 对齐 F-GW-002 的 UsageRecordDraft 与 usage finalization 语义（`docs/specs/streaming-forwarder.md:95-105`, `backend/internal/gateway/forwarder_types.go:78-93`）。

## 4. ProtocolLossEntry

P-0b 扩展现有 `ProtocolLossEntry`，不删除旧字段。新字段满足 Owner 本次要求；旧字段维持已有 tests 和 adapter compile。现有 struct 只有 `Feature/Direction/Verdict/Note`，且 `newLossEntry` 返回旧形态；直接替换会把现有 adapter 和 tests 一次打穿（`backend/internal/proto/proto.go:36-61`, `backend/internal/proto/capability_matrix.go:96-110`, `backend/internal/proto/capability_matrix.go:168-170`, `backend/internal/proto/proto_test.go:126-148`）。

```go
type ProtocolLossSeverity string

const (
	ProtocolLossInfo    ProtocolLossSeverity = "info"
	ProtocolLossWarning ProtocolLossSeverity = "warning"
	ProtocolLossError   ProtocolLossSeverity = "error"
)

type ProtocolLossEntry struct {
	// Field 必填；JSON path / capability field，如 "cache_control.scope"。
	Field string `json:"field"`

	// Vendor 必填；目标 vendor/protocol，如 "openai_chat" / "gemini" / "bedrock_anthropic"。
	Vendor string `json:"vendor"`

	// Severity 必填；info/warning/error。
	Severity ProtocolLossSeverity `json:"severity"`

	// Reason 必填；HUAKAI 自有解释，禁止复制上游源码注释或 identifier。
	Reason string `json:"reason"`

	// Suggestion 可选；如 "use native passthrough at /v1/native/openai/responses"。
	Suggestion string `json:"suggestion,omitempty"`

	// Capability 可选；关联 CapabilityKind。
	Capability CapabilityKind `json:"capability,omitempty"`

	// NodeID 可选；关联 CapabilityNode.ID。
	NodeID string `json:"node_id,omitempty"`

	// NativePath 可选；native_required 时填写建议路径。
	NativePath string `json:"native_path,omitempty"`

	// Code 可选；稳定机器可读码，如 "unsupported_capability"。
	Code string `json:"code,omitempty"`

	// Details 可选；扩展空间，给 D10 matrix cell / D13 gate / D14 测试依赖记录额外信息。
	Details map[string]string `json:"details,omitempty"`

	// Feature 兼容旧字段；P-0b 保留，P-2 后再决定是否迁移调用点。
	Feature string `json:"feature,omitempty"`

	// Direction 兼容旧字段；沿用 client_to_canonical / canonical_to_upstream 等方向。
	Direction string `json:"direction,omitempty"`

	// Verdict 兼容旧字段；沿用 PRESERVED / LOSSY / UNSUPPORTED。
	Verdict Verdict `json:"verdict,omitempty"`

	// Note 兼容旧字段；旧 adapter 可继续填，新增 projection 应优先填 Reason。
	Note string `json:"note,omitempty"`
}
```

Severity 规则：`info` 用于 native suggestion 或 policy note；`warning` 用于 lossy downgrade 但请求可继续；`error` 用于 unsupported 或必须 native passthrough。该规则来自 F-PROTO-002 的 PRESERVED/LOSSY/UNSUPPORTED 矩阵和 failure path，但字段名按本次 Owner 要求升级为 field/vendor/severity/reason/suggestion（`docs/specs/protocol-translation.md:87-124`, `docs/specs/protocol-translation.md:138-151`）。示例 suggestion 固定可用：`use native passthrough at /v1/native/openai/responses`，因为 D11 已批 `/v1/responses` v0.4 仅 native passthrough、不 public（`docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:156-159`）。

## 5. 子结构 (ProviderProjection / StreamPlan / Accounting / Policy)

### 5.1 ProviderProjection

```go
type ProjectionStatus string

const (
	ProjectionSupported        ProjectionStatus = "supported"
	ProjectionLossy            ProjectionStatus = "lossy"
	ProjectionNativeRequired   ProjectionStatus = "native_required"
	ProjectionUnsupportedError ProjectionStatus = "unsupported_error"
	ProjectionRoadmap          ProjectionStatus = "roadmap"
)

type ProviderProjection struct {
	// TargetVendor 必填；如 anthropic/openai/gemini/bedrock。
	TargetVendor string `json:"target_vendor"`

	// TargetProtocol 必填；如 anthropic_messages/openai_chat/openai_responses/gemini_messages。
	TargetProtocol string `json:"target_protocol"`

	// ProtocolFamily 必填；与 gateway registry key 对齐。
	ProtocolFamily string `json:"protocol_family"`

	// Status 必填；整 envelope 对目标 vendor 的总体投影结果。
	Status ProjectionStatus `json:"status"`

	// NativePassthroughRequired 必填；默认 false。
	NativePassthroughRequired bool `json:"native_passthrough_required"`

	// NativePath 可选；NativePassthroughRequired=true 时必须填写。
	NativePath string `json:"native_path,omitempty"`

	// CapabilityResults 必填；每个 present capability node 至少一条结果。
	CapabilityResults []CapabilityProjection `json:"capability_results"`

	// ProtocolLoss 可选；projection 级 loss。
	ProtocolLoss []ProtocolLossEntry `json:"protocol_loss,omitempty"`

	// ToolIDPolicy 可选；D4 留钩子，不在 P-0b 固定 SHA-8/SHA-12。
	ToolIDPolicy ToolIDPolicy `json:"tool_id_policy,omitempty"`
}

type CapabilityProjection struct {
	Capability CapabilityKind `json:"capability"`
	NodeIDs    []string       `json:"node_ids"`
	Status     ProjectionStatus `json:"status"`
	Field      string         `json:"field,omitempty"`
	NativePath string        `json:"native_path,omitempty"`
	ProtocolLoss []ProtocolLossEntry `json:"protocol_loss,omitempty"`
}

type ToolIDPolicy struct {
	MaxDisplayNameBytes int    `json:"max_display_name_bytes,omitempty"`
	HashSuffixLength    int    `json:"hash_suffix_length,omitempty"`
	CollisionPolicy     string `json:"collision_policy,omitempty"`
}
```

`CapabilityResults` 的状态枚举直接锁定 P-5 matrix cell 的五态：supported/lossy/native_required/unsupported_error/roadmap。该五态来自 Codex lane 的 capability matrix strategy，用来保证每个 vendor/capability cell 不会 silent drop（`docs/plans/2026-05-09-hcsf-v04-implementation-codex.md:253-257`, `docs/plans/2026-05-09-hcsf-v04-implementation-codex.md:267-277`）。Tier-A projection 必须覆盖 Anthropic、OpenAI Chat、OpenAI Responses、Gemini、Bedrock-on-Anthropic；D11 决定 OpenAI Responses 只经 `/v1/native/openai/responses` 暴露，不开 public `/v1/responses`（`docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:70-80`, `docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:156-159`）。

### 5.2 StreamPlan

```go
type StreamMode string

const (
	StreamModeBuffered StreamMode = "buffered"
	StreamModeSSE      StreamMode = "sse"
	StreamModeWSS      StreamMode = "wss"
	StreamModeBatch    StreamMode = "batch"
)

type FallbackBoundary string

const (
	FallbackBeforeFirstByte    FallbackBoundary = "before_first_byte"
	FallbackAfterFirstByteBlocked FallbackBoundary = "after_first_byte_blocked"
	FallbackSoftTermination    FallbackBoundary = "soft_termination"
)

type StreamPlan struct {
	// Mode 必填；stream=false 时为 buffered；live_session 默认 wss；batch 默认 batch。
	Mode StreamMode `json:"mode"`

	// Requested 必填；入口请求是否要求流式。
	Requested bool `json:"requested"`

	// EventClasses 必填；可为空数组；记录 message_start/content_delta/message_stop 等 canonical classes。
	EventClasses []string `json:"event_classes"`

	// FlushPolicy 必填；默认 "per_event"。
	FlushPolicy string `json:"flush_policy"`

	// TerminalRequired 必填；默认 true；provider 无终态时按 F-GW-002 合成/标记。
	TerminalRequired bool `json:"terminal_required"`

	// SyntheticTerminalAllowed 必填；默认 true；缺 terminal 时可合成但必须可观测。
	SyntheticTerminalAllowed bool `json:"synthetic_terminal_allowed"`

	// Recoverable 必填；D9 hook；默认 false，P-0b 不实现 mid-stream fallback。
	Recoverable bool `json:"recoverable"`

	// FallbackBoundary 必填；默认 after_first_byte_blocked。
	FallbackBoundary FallbackBoundary `json:"fallback_boundary"`

	// TimeoutPlan 可选；毫秒值，避免 proto 包 import time.Duration 的 JSON 歧义。
	TimeoutPlan StreamTimeoutPlan `json:"timeout_plan,omitempty"`
}

type StreamTimeoutPlan struct {
	FirstTokenTimeoutMS  int64 `json:"first_token_timeout_ms,omitempty"`
	InterEventTimeoutMS  int64 `json:"inter_event_timeout_ms,omitempty"`
	TotalStreamTimeoutMS int64 `json:"total_stream_timeout_ms,omitempty"`
	DrainMaxSeconds      int64 `json:"drain_max_seconds,omitempty"`
	DrainMaxBytes        int64 `json:"drain_max_bytes,omitempty"`
}
```

`StreamPlan` 对齐 F-GW-002 的 per-event flush、terminal marker、stream end taxonomy、bounded drain 和 mid-stream fallback 默认阻断要求（`docs/specs/streaming-forwarder.md:54-85`, `docs/specs/streaming-forwarder.md:87-105`, `docs/specs/streaming-forwarder.md:175-200`）。D9 只通过 `Recoverable` 和 `FallbackBoundary` 留 schema hook；当前合成计划明确中段 fallback 推到 P-8 roadmap，且默认不重试已 flush 的跨 vendor stream（`docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:91-98`, `docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:166-175`）。

### 5.3 Accounting

```go
type Accounting struct {
	// Usage 必填；复用 CanonicalUsage；零值表示尚无 usage。
	Usage CanonicalUsage `json:"usage"`

	// ReasoningUsage 可选；用于 thinking hidden/visible token 拆分。
	ReasoningUsage ReasoningUsage `json:"reasoning_usage,omitempty"`

	// CacheUsage 可选；显式聚合 cache creation/read。
	CacheUsage CacheUsage `json:"cache_usage,omitempty"`

	// StreamEndClass 可选；字符串对齐 gateway.StreamEndClass，避免 proto import gateway。
	StreamEndClass string `json:"stream_end_class,omitempty"`

	// UsageSource 可选；reported/normalized/inferred/partial/ambiguous。
	UsageSource string `json:"usage_source,omitempty"`

	// ActualCost 可选；十进制字符串，避免 proto 包新增 decimal dependency。
	ActualCost string `json:"actual_cost,omitempty"`

	// EvidenceLabel 可选；mock/smoke/real。
	EvidenceLabel EvidenceLabel `json:"evidence_label,omitempty"`

	// BatchUsage 可选；batch capability 使用。
	BatchUsage BatchUsage `json:"batch_usage,omitempty"`

	// LiveUsage 可选；live_session capability 使用。
	LiveUsage LiveUsage `json:"live_usage,omitempty"`
}

type ReasoningUsage struct {
	VisibleTokens int `json:"visible_tokens,omitempty"`
	HiddenTokens  int `json:"hidden_tokens,omitempty"`
	BudgetTokens  int `json:"budget_tokens,omitempty"`
}

type CacheUsage struct {
	CreationInputTokens int `json:"creation_input_tokens,omitempty"`
	ReadInputTokens     int `json:"read_input_tokens,omitempty"`
}

type BatchUsage struct {
	InputItems  int `json:"input_items,omitempty"`
	OutputItems int `json:"output_items,omitempty"`
	FailedItems int `json:"failed_items,omitempty"`
}

type LiveUsage struct {
	DurationMillis int64 `json:"duration_ms,omitempty"`
	InputAudioMillis int64 `json:"input_audio_ms,omitempty"`
	OutputAudioMillis int64 `json:"output_audio_ms,omitempty"`
}
```

`Accounting.Usage` 复用 `CanonicalUsage`，因为该类型已有 input/output/total/cache_creation/cache_read token 字段；`CacheUsage` 是聚合视图，不替代 canonical usage（`backend/internal/proto/hcsf.go:95-107`）。当前 `UsageRecordDraft` 已有 tokens/cache/end_class/usage_source/cost 等字段，HCSFEnvelope 只做内存表达，不改 Tx2 schema（`backend/internal/gateway/forwarder_types.go:78-93`, `docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:127-140`）。

### 5.4 Policy

```go
type AuthPolicy string

const (
	AuthPolicyStandard AuthPolicy = "standard"
)

type AuditVisibility string

const (
	AuditVisible    AuditVisibility = "visible"
	AuditRedacted   AuditVisibility = "redacted"
	AuditInternal   AuditVisibility = "internal"
)

type Policy struct {
	// DataRetention 必填；默认 unknown。
	DataRetention DataRetentionNode `json:"data_retention"`

	// Auth 必填；D5 锁定 standard。
	Auth AuthPolicy `json:"auth"`

	// Audit 必填；native passthrough 必须可审计。
	Audit AuditPolicy `json:"audit"`

	// Redaction 必填；默认 public。
	Redaction RedactionClass `json:"redaction"`

	// NativePassthroughAllowed 必填；默认 false；route 实现仍需 P-4 Owner gate。
	NativePassthroughAllowed bool `json:"native_passthrough_allowed"`

	// ProtocolLoss 可选；policy 层 loss，如 ZDR 未证明。
	ProtocolLoss []ProtocolLossEntry `json:"protocol_loss,omitempty"`
}

type AuditPolicy struct {
	Visibility AuditVisibility `json:"visibility"`
	Label      string          `json:"label"`
	Reason     string          `json:"reason,omitempty"`
}
```

`Policy.Auth` 只允许 `standard`，因为 D5 已批 native passthrough 使用 standard route auth 并增加 audit；P-0b 不实现 auth core 变化（`docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:142-145`）。`DataRetention.Value` 锁定 D12 五词汇，`zdr_verified` 必须有 Owner/vendor/account proof，不能从 generic request field 推断（`docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:161-164`, `docs/plans/2026-05-09-hcsf-v04-implementation-codex.md:344-345`, `docs/plans/2026-05-09-hcsf-v04-implementation-codex.md:362-368`）。

## 6. 命名空间清理 (proto.HCSF / proto.UpstreamState / proto.ContentBlock 等)

1. `proto.HCSF struct{}` 不删除成无类型空洞；P-0b 将它改成 alias：`type HCSF = HCSFEnvelope`。原因是当前 `ClientAdapter`、`UpstreamAdapter`、OpenAI/Gemini/Bedrock adapter 签名都使用 `*HCSF`，alias 可以避免一轮大范围接口 rename（`backend/internal/proto/proto.go:20-34`, `backend/internal/proto/openai_sse.go:142-156`, `backend/internal/proto/gemini_sse.go:97-111`, `backend/internal/proto/bedrock_eventstream.go:67-75`）。

2. `proto.UpstreamState`、`OpenAIUpstreamState`、`GeminiUpstreamState` 继续是 provider stream state，不折进 `HCSFEnvelope`。Envelope 只通过 `RequestMeta.AccountID`、`RequestMeta.SessionHash`、`Accounting.Usage` 表达跨层结果；state 仍由 forwarder per stream 构造。原因是 forwarder 已按 adapter 实际类型选择 state，且这些 state 带 per-stream mutable 字段，放入 envelope 会让 IR 与 streaming hot path 生命周期混在一起（`backend/internal/gateway/forwarder.go:324-351`, `backend/internal/proto/anthropic_sse.go:16-39`, `backend/internal/proto/openai_sse.go:24-47`, `backend/internal/proto/gemini_sse.go:29-56`）。

3. `CanonicalMessage` / `CanonicalContentBlock` / `CanonicalEvent` / `CanonicalResponse` / `CanonicalUsage` 不删除。P-0b 的 envelope 复用它们：`Messages []CanonicalMessage`、`BufferedResponse *CanonicalResponse`、`StreamEvents []CanonicalEvent`、`Accounting.Usage CanonicalUsage`。原因是当前 provider-to-canonical tests 和 forwarder hot path 已围绕这些类型运行，一次性替换会扩大风险（`backend/internal/proto/hcsf.go:36-107`, `backend/internal/gateway/forwarder.go:215-241`, `backend/internal/proto/proto_test.go:44-66`）。

4. 当前仓库没有名为 `proto.ContentBlock` / `proto.Response` / `proto.Usage` 的裸类型；实际名称是 `CanonicalContentBlock` / `CanonicalResponse` / `CanonicalUsage`。P-0b 文档和代码应使用实际名称，避免生成不存在的 alias（`backend/internal/proto/hcsf.go:42-107`）。

## 7. 兼容性迁移路径

P-0b 是 schema lock，不是 dispatcher/forwarder 大改。迁移分四步：

1. **P-0b alias 兼容**：新增 `HCSFEnvelope`，将 `type HCSF = HCSFEnvelope` 放在 `proto.go` 或新 `hcsf_envelope.go`。现有 adapter 签名不改；OpenAI/Gemini `ProviderResponseToCanonical` 可开始填 `BufferedResponse`，但不要求 P-0b 改全部逻辑（`backend/internal/proto/proto.go:20-34`, `backend/internal/proto/openai_sse.go:148-156`, `backend/internal/proto/gemini_sse.go:103-111`）。

2. **P-0b fixture/helper**：只加 envelope constructor/validation/round-trip tests，不接 forwarder hot path。当前 forwarder 已有 nil ClientAdapter fallback 和 per-adapter UpstreamState 选择，P-0b 不碰这些生产路径（`backend/internal/gateway/forwarder.go:293-299`, `backend/internal/gateway/forwarder.go:324-351`）。

3. **P-1 graph builder**：从 `CanonicalRequest` / `CanonicalEvent` / `CanonicalResponse` 生成 `CapabilityGraph`，并把旧 CapabilityMatrix 的 feature-level verdict 映射到新 `ProviderProjection.CapabilityResults`。当前 CapabilityMatrix 是粗粒度默认 PRESERVED 后少量改 LOSSY/UNSUPPORTED，P-1 需要测试驱动重建（`backend/internal/proto/capability_matrix.go:73-110`, `docs/research/2026-05-09-axis3-huakai-current-state.md:326-328`）。

4. **P-2/P-3 adapter 收口**：ClientAdapter 与 Provider render path 逐步改用 `*HCSFEnvelope` 命名；完成后可把接口签名从 `*HCSF` 改到 `*HCSFEnvelope`。当前 ClientAdapter concrete 实现为 0，CanonicalToProviderRequest 全部 ErrNotImplemented，所以迁移必须跟 P-2/P-3 分开做（`docs/research/2026-05-09-axis3-huakai-current-state.md:182-190`, `docs/research/2026-05-09-axis3-huakai-current-state.md:221-225`, `docs/research/2026-05-09-axis3-huakai-current-state.md:274-298`）。

`pasr_selector` / selector dispatcher 不直接消费 envelope；它通过 `ForwardRequest.SessionHash`、UpstreamState 的 `PrefixHash`、cachemetrics feedback 关联 cache locality。P-0b 只在 `RequestMeta.SessionHash` 和 `CacheControlNode` 记录相同信息，不改变 PASR 数据路径（`backend/internal/gateway/forwarder_types.go:122-126`, `backend/internal/proto/anthropic_sse.go:22-38`, `backend/internal/proto/openai_sse.go:40-47`, `backend/internal/proto/gemini_sse.go:46-55`）。

## 8. JSON Round-Trip 不变量

P-0b 必须写 round-trip 测试，不变量如下：

1. 构造 envelope 时，所有 required slice 字段使用非 nil 空数组：`Messages`、`CapabilityGraph.Nodes`、`CapabilityGraph.Edges`、`ProviderProjection.CapabilityResults`、`StreamPlan.EventClasses`。这样第一次 marshal 不会把 required arrays 编成 `null`，也不会在 unmarshal 后出现 nil/empty 语义漂移。

2. 测试流程固定为：`b1 := json.Marshal(env)`，`json.Unmarshal(b1, &decoded)`，`b2 := json.Marshal(decoded)`，断言 `bytes.Equal(b1, b2)`。如果某 fixture 使用 `map[string]json.RawMessage`，Go `encoding/json` 会稳定排序 map key；若未来引入自定义 marshal，仍必须保持该测试通过。

3. 同时断言 `reflect.DeepEqual(env, decoded)` 或对 RawMessage 做 normalize 后 deep equal。RawMessage 输入必须是 canonical JSON，禁止带无意义空格来制造 byte-level 差异。

4. `omitempty` 只能用于 optional 字段；required 字段即使零值也必须输出。特别是 `Version`、`RequestMeta`、`RequestControls`、`Messages`、`CapabilityGraph`、`ProviderProjection`、`StreamPlan`、`Accounting`、`Policy` 不允许 `omitempty`。

该测试属于 P-0 schema shape test。Codex lane 已要求 P-0 输出 compile-level schema JSON shape、loss severity validation、text-only canonical event round-trip 稳定性；P-5 再扩大为 capability/property matrix（`docs/plans/2026-05-09-hcsf-v04-implementation-codex.md:114-120`, `docs/plans/2026-05-09-hcsf-v04-implementation-codex.md:188-202`, `docs/specs/protocol-translation.md:175-197`）。

## 9. 测试 fixture 要求

P-0b 最少 fixture 集：

1. `empty_graph_minimal`: version/request_meta/request_controls/messages empty/capability_graph empty/provider_projection empty results/stream_plan buffered/accounting zero/policy unknown。覆盖 empty graph。

2. `single_text_sse`: one user text message + `TextNode` + `StreamPlan.Mode=sse` + no loss。覆盖 text node 与现有 CanonicalMessage/CanonicalContentBlock 复用。

3. `tool_use_chain`: assistant text -> tool_use -> tool_result，包含 `MessageToToolUseEdge` 与 `ToolUseToToolResultEdge`，断言 tool_call_id round-trip 字段存在。

4. 每个 concrete node payload 至少一个 minimal fixture：text、tool_use、tool_result、thinking、cache_control、structured_output、computer_use、file、image、audio、video、live_session、batch、mcp_server、data_retention。file/image/audio/video 必须分开，禁止以 multimodal 一条 fixture 代替。

5. per-vendor projection fixture：`anthropic_messages_rich`、`openai_chat_lossy_thinking_cache`、`openai_responses_native_required`、`gemini_live_native_required`、`bedrock_anthropic_rich_or_unknown_cache`。每个 fixture 的 `CapabilityResults` 对 present nodes 必须逐一给出 supported/lossy/native_required/unsupported_error/roadmap。

6. 边界 fixture：`native_passthrough_required_openai_responses` 必须含 `ProtocolLossEntry{field:"/v1/responses", vendor:"openai_responses", severity:"error", suggestion:"use native passthrough at /v1/native/openai/responses"}`；`cache_sanitizer_new_api_4678` 必须含 `CacheControlNode.SanitizeSystemMetadata=true`；`stream_recoverable_false_after_flush` 必须含 `StreamPlan.Recoverable=false` 与 `FallbackBoundary=after_first_byte_blocked`。

这些 fixture 对应当前 issue mining 的核心痛点：Responses API 需求、cache_read/cache header 破坏、stream terminal/fallback、tool call 回归、MCP/batch/file/image 新需求（`docs/research/2026-05-09-issue-mining-cross-repo.md:33-43`, `docs/research/2026-05-09-issue-mining-cross-repo.md:69-70`, `docs/research/2026-05-09-issue-mining-cross-repo.md:112-117`, `docs/research/2026-05-09-issue-mining-cross-repo.md:130-143`, `docs/research/2026-05-09-issue-mining-cross-repo.md:204-218`）。

## 10. 推迟决策点的 schema 留空间

| 推迟点 | P-0b schema hook | 本阶段不做 |
|---|---|---|
| D4 SHA-8 vs SHA-12 | `ToolUseNode.OriginalToolCallID`、`ToolUseNode.DisplayName`、`ProviderProjection.ToolIDPolicy.HashSuffixLength` | 不固定 hash 长度，不写 collision algorithm |
| D8 spend 数字来源 | `EvidenceLabel` 出现在 RequestMeta/Accounting | 不声明真实 spend，不写 dashboard |
| D9 mid-stream fallback | `StreamPlan.Recoverable`、`StreamPlan.FallbackBoundary` | 不实现 mid-stream fallback |
| D10 matrix cell 数 | `ProviderProjection.CapabilityResults` + `ProtocolLossEntry.Details` | 不固定最终 cell 总数 |
| D13 P-5 release gate | `ProjectionStatus` 五态 + fixture 命名 | 不定 release threshold |
| D14 测试依赖 | schema 全部使用 stdlib JSON-friendly 类型 | 不新增 runtime dependency；test-only dependency 也需 Owner 另批 |

这些 postponed hooks 与合成计划一致：D4/D8/D9/D10/D13/D14 都推到 implementation phase，不在 P-0 重新评估（`docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:166-175`）。不新增 runtime dependency 也符合 Codex lane 对 P-0/P-5 测试依赖的风险提示（`docs/plans/2026-05-09-hcsf-v04-implementation-codex.md:352-352`）。

## 风险与盲点

1. `feedback_chinese_comments.md` 未在仓库中定位到；我按仓库中已有 Owner 记忆记录执行中文注释约束，但无法引用该原文件本体（`docs/plans/2026-05-08-pasr-lite-v2-codex.md:154`, `docs/plans/2026-05-09-next-pivot-claude.md:228`）。

2. 本 spec 是 schema 草案，不是实现。它不跑 `go test`，不更新 `backend/internal/proto`，不修改 database schema。P-0b executor 仍需做 compile/test/codex review 流程。

3. `ProtocolLossEntry` 新旧字段共存会短期冗余，但这是最小破坏迁移。直接删除旧字段会打断当前 adapter/tests；保留旧字段可以让 P-0b 小步落地（`backend/internal/proto/proto.go:36-61`, `backend/internal/proto/proto_test.go:126-148`）。

4. `CapabilityNode` tagged-union 需要 validator 保证 exactly-one payload。P-0b 若只写 struct 不写 validator，JSON round-trip 能过，但非法 node 可能进入后续 phase；因此 validator 是 P-0b 同文件测试的必要配套。

5. No clean-room reference source was read in this lane. This file relies on HUAKAI internal synthesis/research artifacts and local HUAKAI code only.

## Source citations

- `docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:23-36` — P-0/P-7 phase split and Owner gates.
- `docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:38-64` — 14 capability synthesis and D2 recommendation.
- `docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:81-98` — test layers and failure modes.
- `docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:123-175` — approved D1/D2/D3/D5/D6/D7/D11/D12 and deferred D4/D8/D9/D10/D13/D14.
- `docs/plans/2026-05-09-hcsf-v04-implementation-codex.md:20-68` — Codex lane capability graph/envelope/protocol_loss proposal.
- `docs/plans/2026-05-09-hcsf-v04-implementation-codex.md:91-120` — P-0 outputs and compatibility risk.
- `docs/plans/2026-05-09-hcsf-v04-implementation-codex.md:251-263` — test strategy and real smoke labeling.
- `docs/plans/2026-05-09-hcsf-v04-implementation-codex.md:334-352` — decision points including field names, DB, responses, data retention, smoke, native auth, dependency.
- `docs/plans/2026-05-09-hcsf-canonical-synthesis.md:66-123` — approved architecture: OpenAI storefront + Anthropic side-entry + capability graph + native passthrough.
- `docs/research/2026-05-09-axis3-huakai-current-state.md:14-28` — current proto inventory and HCSF wrapper gap.
- `docs/research/2026-05-09-axis3-huakai-current-state.md:81-100` — vendor coverage and ClientAdapter absence.
- `docs/research/2026-05-09-axis3-huakai-current-state.md:106-138` — Canonical* current shape and wrapper mismatch.
- `docs/research/2026-05-09-axis3-huakai-current-state.md:142-167` — UpstreamState/tool-call gaps.
- `docs/research/2026-05-09-axis3-huakai-current-state.md:182-225` — nil ClientAdapter and Phase A/B/D/E gaps.
- `docs/research/2026-05-09-axis3-huakai-current-state.md:236-270` — current test/smoke gaps.
- `docs/research/2026-05-09-axis3-huakai-current-state.md:274-320` — top five Axis-3 gaps.
- `docs/research/2026-05-09-issue-mining-cross-repo.md:33-43` — systemic issue TL;DR and HCSF implications.
- `docs/research/2026-05-09-issue-mining-cross-repo.md:78-95` — sub2api issue-derived protocol/image/cache/stream pain points.
- `docs/research/2026-05-09-issue-mining-cross-repo.md:102-120` — new-api cache/stream/Responses/tool pain points.
- `docs/research/2026-05-09-issue-mining-cross-repo.md:126-145` — Portkey cache/tool/Responses/MCP/batch pain points.
- `docs/research/2026-05-09-issue-mining-cross-repo.md:204-218` — streaming/tool/cache distribution and HCSF requirements.
- `docs/research/2026-05-09-issue-mining-cross-repo.md:241-249` — HUAKAI must handle schema drift, mid-stream, cache sanitizer, data retention/failure billing hooks.
- `backend/internal/proto/proto.go:13-61` — empty HCSF, adapter interfaces, current ProtocolLossEntry/Verdict/Direction.
- `backend/internal/proto/hcsf.go:11-119` — current CanonicalRequest/Message/ContentBlock/Event/Response/Usage.
- `backend/internal/proto/anthropic_sse.go:16-39` — Anthropic UpstreamState fields and PASR/cache feedback fields.
- `backend/internal/proto/openai_sse.go:24-47` — OpenAIUpstreamState.
- `backend/internal/proto/gemini_sse.go:29-56` — GeminiUpstreamState.
- `backend/internal/proto/bedrock_eventstream.go:67-90` — Bedrock adapter request/response TODO and state contract.
- `backend/internal/proto/capability_matrix.go:30-110` — current feature matrix and old loss generation.
- `backend/internal/proto/field_matrix.go:1-19` — field matrix intent and preserve-by-default audit semantics.
- `backend/internal/gateway/forwarder.go:41-43` — optional ClientAdapter.
- `backend/internal/gateway/forwarder.go:79-94` — protocol family fail-loud registry/scanner selection.
- `backend/internal/gateway/forwarder.go:215-241` — provider event to canonical to client chunk hot path.
- `backend/internal/gateway/forwarder.go:293-299` — nil ClientAdapter raw SSE fallback.
- `backend/internal/gateway/forwarder.go:324-351` — per-adapter UpstreamState construction.
- `backend/internal/gateway/forwarder_types.go:78-127` — UsageRecordDraft and ForwardRequest metadata.
- `backend/internal/gateway/upstream_dispatcher.go:37-52` — DispatchInput and ProtocolFamily.
- `backend/internal/gateway/upstream_dispatcher.go:102-149` — dispatcher adapter/request/transport/http flow.
- `docs/specs/protocol-translation.md:43-85` — F-PROTO normal path.
- `docs/specs/protocol-translation.md:87-124` — capability matrix and protocol_loss schema.
- `docs/specs/protocol-translation.md:138-151` — failure paths for unsupported/lossy/unknown events.
- `docs/specs/protocol-translation.md:175-197` — acceptance tests including matrix and protocol_loss.
- `docs/specs/streaming-forwarder.md:54-105` — stream event processing, end classification, drain, Tx2.
- `docs/specs/streaming-forwarder.md:175-200` — streaming acceptance tests and mid-stream fallback boundaries.

## Tail block (per AGENTS.md template)

Source files read: HUAKAI internal docs/code only: `docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md`; `docs/plans/2026-05-09-hcsf-v04-implementation-codex.md`; `docs/plans/2026-05-09-hcsf-canonical-synthesis.md`; `docs/research/2026-05-09-axis3-huakai-current-state.md`; `docs/research/2026-05-09-issue-mining-cross-repo.md`; `docs/specs/protocol-translation.md`; `docs/specs/streaming-forwarder.md`; `backend/internal/proto/proto.go`; `backend/internal/proto/hcsf.go`; `backend/internal/proto/anthropic_sse.go`; `backend/internal/proto/openai_sse.go`; `backend/internal/proto/gemini_sse.go`; `backend/internal/proto/bedrock_eventstream.go`; `backend/internal/proto/capability_matrix.go`; `backend/internal/proto/field_matrix.go`; `backend/internal/gateway/forwarder.go`; `backend/internal/gateway/forwarder_types.go`; `backend/internal/gateway/upstream_dispatcher.go`.
Lane: codex independent schema-spec (HUAKAI-internal only)
Agent: GPT-5 Codex
UTC timestamp: 2026-05-09T17:38:49Z
