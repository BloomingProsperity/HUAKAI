package proto

// ProtocolLossSeverity 是 v0.4 ProtocolLossEntry 的严重等级。
//
//   - info：native 建议或 policy note；请求可继续。
//   - warning：lossy downgrade 但请求可继续。
//   - error：unsupported 或必须走 native passthrough。
type ProtocolLossSeverity string

const (
	ProtocolLossInfo    ProtocolLossSeverity = "info"
	ProtocolLossWarning ProtocolLossSeverity = "warning"
	ProtocolLossError   ProtocolLossSeverity = "error"
)

// ProtocolLossEntry 是 envelope/capability/projection 三层一等公民。
//
// v0.4 在保留 v0.3 旧字段（feature/direction/verdict/note）的同时，新增 v0.4 字段
// （field/vendor/severity/reason/suggestion + capability/node_id/native_path/code/details）。
// 这样旧 adapter（capability_matrix / anthropic_sse / openai_sse / gemini_sse）继续编译，
// 新代码可优先填 Reason 与 Severity。
//
// 任何 capability 在任何 provider projection 上有 lossy 表现 → 必须 emit 一条
// ProtocolLossEntry；不可作为 silent drop（INV-7）。
type ProtocolLossEntry struct {
	// ---- v0.4 新字段 ----

	// Field 可选（v0.4 推荐填）；JSON path / capability field，如 "cache_control.scope"。
	Field string `json:"field,omitempty"`

	// Vendor 可选（v0.4 推荐填）；目标 vendor/protocol，如 openai_chat / gemini / bedrock_anthropic。
	Vendor string `json:"vendor,omitempty"`

	// Severity 可选（v0.4 推荐填）；info/warning/error。
	Severity ProtocolLossSeverity `json:"severity,omitempty"`

	// Reason 可选（v0.4 推荐填）；HUAKAI 自有解释，禁止复制上游源码注释或 identifier。
	Reason string `json:"reason,omitempty"`

	// Suggestion 可选；如 "use native passthrough at /v1/native/openai/responses"。
	Suggestion string `json:"suggestion,omitempty"`

	// Capability 可选；关联 CapabilityKind。
	Capability CapabilityKind `json:"capability,omitempty"`

	// NodeID 可选；关联 CapabilityNode.ID。
	NodeID string `json:"node_id,omitempty"`

	// NativePath 可选；native_required 时填写建议路径。
	NativePath string `json:"native_path,omitempty"`

	// Code 可选；稳定机器可读码，如 unsupported_capability。
	Code string `json:"code,omitempty"`

	// Details 可选；扩展空间，给 D10 matrix cell / D13 gate / D14 测试依赖记录信息。
	Details map[string]string `json:"details,omitempty"`

	// ---- v0.3 兼容字段（旧 adapter 继续可用，P-2 后再决定迁移） ----

	// Feature 兼容旧字段；保留 string 形态以容纳 FeatureName 来源。
	Feature string `json:"feature,omitempty"`

	// Direction 兼容旧字段；沿用 client_to_canonical / canonical_to_upstream 等方向。
	Direction string `json:"direction,omitempty"`

	// Verdict 兼容旧字段；PRESERVED / LOSSY / UNSUPPORTED。
	Verdict Verdict `json:"verdict,omitempty"`

	// Note 兼容旧字段；旧 adapter 可继续填，新代码应优先填 Reason。
	Note string `json:"note,omitempty"`
}

// IsSilentDrop 判断条目是否为静默丢失（既无 v0.3 verdict/note 也无 v0.4 reason）。
// envelope_validate 用它来强制 INV-7。
func (e ProtocolLossEntry) IsSilentDrop() bool {
	return e.Reason == "" && e.Note == "" && e.Verdict == "" && e.Code == ""
}
