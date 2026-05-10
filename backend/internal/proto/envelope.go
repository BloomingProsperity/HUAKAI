package proto

import "encoding/json"

// HCSFVersion 是 HCSFEnvelope schema 锁定版本号；P-0 阶段不允许写入其它值。
const HCSFVersion = "0.4"

// HCSFEnvelope 是 HCSF v0.4 的顶层载体；P-0 仅作为内存 IR，不入库不持久化。
//
// 形态推导规则（不引入 EnvelopeKind 枚举字段）：
//
//   - 仅 Messages 非空 + BufferedResponse=nil + StreamEvents=nil → request envelope
//   - BufferedResponse != nil → buffered response envelope
//   - StreamEvents != nil → event-replay envelope（fixture / replay 用途）
//   - Native Passthrough → RequestMeta.NativePassthrough=true；route 实现见 P-4 决策
//
// BufferedResponse 与 StreamEvents 至多一个非 nil（见 INV-6）。
type HCSFEnvelope struct {
	// Version 必填；锁定 "0.4"；envelope_validate 强校验。
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

	// Extensions 可选；key 必须以 vendor: 或 experimental: 前缀（INV-12）。
	// 不得用于隐藏 capability drop；任何 capability lossy 必须发 ProtocolLossEntry。
	Extensions map[string]json.RawMessage `json:"extensions,omitempty"`
}

// NewEmptyEnvelope 构造一个最小合法 envelope，主要用于测试与 fixture 起点。
// 所有 required slice 字段使用非 nil 空数组（INV-1 / round-trip 稳定性）。
func NewEmptyEnvelope() *HCSFEnvelope {
	return &HCSFEnvelope{
		Version:         HCSFVersion,
		RequestMeta:     RequestMeta{},
		RequestControls: RequestControls{},
		Messages:        []CanonicalMessage{},
		CapabilityGraph: CapabilityGraph{
			Nodes: []CapabilityNode{},
			Edges: []CapabilityEdge{},
		},
		ProviderProjection: ProviderProjection{
			CapabilityResults: []CapabilityProjection{},
		},
		StreamPlan: StreamPlan{
			Mode:                     StreamModeBuffered,
			EventClasses:             []string{},
			FlushPolicy:              "per_event",
			TerminalRequired:         true,
			SyntheticTerminalAllowed: true,
			FallbackBoundary:         FallbackAfterFirstByteBlocked,
			MidStreamFallbackPolicy:  MidStreamFallbackNone,
		},
		Accounting: Accounting{},
		Policy: Policy{
			DataRetention: DataRetentionNode{
				Value:       DataRetentionUnknown,
				Enforcement: "unknown",
				AuditLabel:  "unknown",
			},
			Auth:     AuthPolicyStandard,
			Audit:    AuditPolicy{Visibility: AuditVisible, Label: "default"},
			Redaction: RedactionPublic,
		},
	}
}
