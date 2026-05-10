package proto

import "encoding/json"

// EvidenceLabel 标记 envelope 各项数据的证据等级；P-0 默认 mock。
type EvidenceLabel string

const (
	EvidenceMock  EvidenceLabel = "mock"
	EvidenceSmoke EvidenceLabel = "smoke"
	EvidenceReal  EvidenceLabel = "real"
)

// RequestMeta 记录入口协议、路由身份、上游协议族和 request 级审计锚点。
//
// 与 ForwardRequest 的关系是映射而非替换：
// 现有 ForwardRequest 已带 TenantID/AccountID/AcquisitionToken/RouteID/ProtocolFamily
// 等字段，envelope 在内存 IR 层用 RequestMeta 复刻一份，便于 fixture 与 projection 推导。
type RequestMeta struct {
	// RequestID 必填；HUAKAI 内部 request 追踪 ID；没有上游 ID 时由入口生成。
	RequestID string `json:"request_id"`

	// TenantID 可选；0 表示无租户上下文；P-0 不改变 auth/tenant 解析逻辑。
	TenantID int64 `json:"tenant_id,omitempty"`

	// RouteID 可选；沿用 gateway ForwardRequest.RouteID 的语义。
	RouteID string `json:"route_id,omitempty"`

	// AccountID 可选；选中 provider_account_id；仅用于审计/usage/PASR 反馈关联。
	AccountID int64 `json:"account_id,omitempty"`

	// AcquisitionToken 可选；字符串化 UUID；避免 proto 包新增 uuid runtime dependency。
	AcquisitionToken string `json:"acquisition_token,omitempty"`

	// ClientProtocol 必填；合法值沿用 openai_chat/openai_responses/anthropic_messages。
	ClientProtocol ClientProtocol `json:"client_protocol"`

	// ProtocolFamily 必填；forwarder/dispatcher 用它选择 upstream adapter。
	ProtocolFamily string `json:"protocol_family"`

	// UpstreamProtocol 可选；用于 capability matrix 与 projection。
	UpstreamProtocol UpstreamProtocol `json:"upstream_protocol,omitempty"`

	// Provider 可选；人读 vendor 名，如 anthropic / openai / gemini / bedrock。
	Provider string `json:"provider,omitempty"`

	// Model 必填；入口模型名。
	Model string `json:"model"`

	// UpstreamModel 可选；registry 解析后的真实上游模型名。
	UpstreamModel string `json:"upstream_model,omitempty"`

	// IngressPath 必填；如 /v1/chat/completions、/v1/messages、/v1/native/openai/responses。
	IngressPath string `json:"ingress_path"`

	// IdempotencyKey 可选；保留未来 batch/live/retry 幂等钩子。
	IdempotencyKey string `json:"idempotency_key,omitempty"`

	// SessionHash 可选；当前用于 PASR prefix feedback；空值表示不触发 prefix 更新。
	SessionHash string `json:"session_hash,omitempty"`

	// NativePassthrough 必填；默认 false；true 表示本 envelope 是 native route 的审计/投影壳。
	NativePassthrough bool `json:"native_passthrough"`

	// EvidenceLabel 可选；默认 mock；P-6 后才可写 smoke 或 real。
	EvidenceLabel EvidenceLabel `json:"evidence_label,omitempty"`
}

// RequestControls 显式承接现有 CanonicalRequest 中的请求控制字段，避免 envelope
// 只保留 messages 后丢请求控制项。
//
// Stream 移入 StreamPlan；Model/UpstreamModel 移入 RequestMeta；Messages 保持顶层一等字段。
type RequestControls struct {
	// Tools 可选；复用 CanonicalTool；空数组表示无工具声明。
	Tools []CanonicalTool `json:"tools,omitempty"`

	// ToolChoice 可选；保留 canonical JSON，不在 P-0 决定各 vendor tool_choice dialect。
	ToolChoice json.RawMessage `json:"tool_choice,omitempty"`

	// MaxTokens 可选；nil 表示未指定，避免与 0 混淆。
	MaxTokens *int `json:"max_tokens,omitempty"`

	// Stop 可选；空数组表示未指定（OpenAI 风格 stop 字段）。
	Stop []string `json:"stop,omitempty"`

	// StopSequences 可选；空数组表示未指定（Anthropic 风格 stop_sequences 字段）。
	StopSequences []string `json:"stop_sequences,omitempty"`

	// Temperature 可选；nil 表示未指定，不能与 0.0 混淆。
	Temperature *float64 `json:"temperature,omitempty"`

	// TopP 可选；nil 表示未指定，不能与 0.0 混淆。
	TopP *float64 `json:"top_p,omitempty"`

	// SystemPrompt 可选；空串表示未指定；系统内容的 cache sanitizer 由 cache_control node 表达。
	SystemPrompt string `json:"system_prompt,omitempty"`

	// ParallelToolCalls 可选；nil 表示 provider 默认。
	ParallelToolCalls *bool `json:"parallel_tool_calls,omitempty"`

	// ResponseFormat 可选；用 RawMessage 保留 OpenAI/Gemini/Anthropic schema dialect。
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`

	// Seed 可选；nil 表示未指定。
	Seed *int `json:"seed,omitempty"`

	// ToolNameHashAlgorithm 可选；D4 推迟决策点 schema 留位（默认空 = sha8 隐含约定）。
	// P-2/P-3 时再具体选 sha8/sha12，并在此处显式写入。
	ToolNameHashAlgorithm string `json:"tool_name_hash_algorithm,omitempty"`
}

// ResponseFormat 是结构化输出/JSON mode 的载体；用 RawMessage 容纳各 vendor dialect。
type ResponseFormat struct {
	// Type 必填；如 json_object / json_schema / text。
	Type string `json:"type"`

	// Schema 可选；vendor-specific schema body。
	Schema json.RawMessage `json:"schema,omitempty"`

	// Strict 可选；nil 表示 provider 默认。
	Strict *bool `json:"strict,omitempty"`
}
