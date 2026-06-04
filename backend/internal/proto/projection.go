package proto

// ProjectionVerdict 表达 capability 在某 vendor projection 上的可行度。
type ProjectionVerdict string

const (
	// ProjectionPreserved 保真投影；无 lossy。
	ProjectionPreserved ProjectionVerdict = "preserved"

	// ProjectionLossy 有损投影；必须发 ProtocolLossEntry。
	ProjectionLossy ProjectionVerdict = "lossy"

	// ProjectionUnsupported 不支持；adapter 需返回错误或建议 native。
	ProjectionUnsupported ProjectionVerdict = "unsupported"

	// ProjectionNativeRequired 必须走 native passthrough 才能保留。
	ProjectionNativeRequired ProjectionVerdict = "native_required"
)

// CapabilityProjection 描述 envelope 中某个 capability node 的 projection 结果。
type CapabilityProjection struct {
	// Capability 必填；CapabilityKind enum。
	Capability CapabilityKind `json:"capability"`

	// NodeID 可选；关联 CapabilityNode.ID（图级 projection 可省）。
	NodeID string `json:"node_id,omitempty"`

	// Verdict 必填；preserved/lossy/unsupported/native_required。
	Verdict ProjectionVerdict `json:"verdict"`

	// ProtocolLoss 可选；当 Verdict 非 preserved 时**必须**至少一条。
	ProtocolLoss []ProtocolLossEntry `json:"protocol_loss,omitempty"`

	// NativePath 可选；native_required 时必填，如 /v1/native/openai/responses。
	NativePath string `json:"native_path,omitempty"`
}

// ProviderProjection 是 envelope 在目标 vendor / protocol 上的整体投影记录。
type ProviderProjection struct {
	// TargetVendor 可选；如 anthropic / openai / gemini / bedrock_anthropic。
	TargetVendor string `json:"target_vendor,omitempty"`

	// TargetProtocol 可选；UpstreamProtocol enum；P-1 后由 dispatcher 写入。
	TargetProtocol UpstreamProtocol `json:"target_protocol,omitempty"`

	// CapabilityResults 必填；可为空数组；每个 capability node 的 projection 结果。
	CapabilityResults []CapabilityProjection `json:"capability_results"`

	// OverallVerdict 可选；从 CapabilityResults 派生的全局裁决（最严重那条）。
	OverallVerdict ProjectionVerdict `json:"overall_verdict,omitempty"`
}
