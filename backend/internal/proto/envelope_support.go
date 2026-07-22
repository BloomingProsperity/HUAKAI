package proto

// Accounting 保存信封的用量、证据与信任链信息，不负责实际定价。
type Accounting struct {
	Usage             CanonicalUsage   `json:"usage,omitempty"`
	UsageSource       string           `json:"usage_source,omitempty"`
	ReasoningTokens   int              `json:"reasoning_tokens,omitempty"`
	LiveUsage         *LiveUsage       `json:"live_usage,omitempty"`
	BatchUsage        *BatchUsage      `json:"batch_usage,omitempty"`
	EvidenceLabel     EvidenceLabel    `json:"evidence_label,omitempty"`
	HopChain          []HopAttestation `json:"hop_chain,omitempty"`
	ModelChain        *ModelChain      `json:"model_chain,omitempty"`
	LedgerID          string           `json:"ledger_id,omitempty"`
	Signature         string           `json:"signature,omitempty"`
	PubkeyFingerprint string           `json:"pubkey_fp,omitempty"`
}

// LiveUsage 是实时会话的用量明细。
type LiveUsage struct {
	SessionDurationMS int64 `json:"session_duration_ms,omitempty"`
	InputAudioMS      int64 `json:"input_audio_ms,omitempty"`
	OutputAudioMS     int64 `json:"output_audio_ms,omitempty"`
}

// BatchUsage 是批处理的用量明细。
type BatchUsage struct {
	Inputs  int `json:"inputs,omitempty"`
	Outputs int `json:"outputs,omitempty"`
	Errors  int `json:"errors,omitempty"`
}

// ProjectionVerdict 表达能力投影到目标协议后的可行度。
type ProjectionVerdict string

const (
	ProjectionPreserved      ProjectionVerdict = "preserved"
	ProjectionLossy          ProjectionVerdict = "lossy"
	ProjectionUnsupported    ProjectionVerdict = "unsupported"
	ProjectionNativeRequired ProjectionVerdict = "native_required"
)

// CapabilityProjection 记录一个能力节点的目标协议投影结果。
type CapabilityProjection struct {
	Capability   CapabilityKind      `json:"capability"`
	NodeID       string              `json:"node_id,omitempty"`
	Verdict      ProjectionVerdict   `json:"verdict"`
	ProtocolLoss []ProtocolLossEntry `json:"protocol_loss,omitempty"`
	NativePath   string              `json:"native_path,omitempty"`
}

// ProviderProjection 汇总整个信封在目标供应商协议上的投影结果。
type ProviderProjection struct {
	TargetVendor      string                 `json:"target_vendor,omitempty"`
	TargetProtocol    UpstreamProtocol       `json:"target_protocol,omitempty"`
	CapabilityResults []CapabilityProjection `json:"capability_results"`
	OverallVerdict    ProjectionVerdict      `json:"overall_verdict,omitempty"`
}

// AuthPolicy 是信封认证状态。
type AuthPolicy string

const (
	AuthPolicyStandard AuthPolicy = "standard"
	AuthPolicyRevoked  AuthPolicy = "revoked"
)

// AuditVisibility 决定运维面显示的内容级别。
type AuditVisibility string

const (
	AuditVisible AuditVisibility = "visible"
	AuditMasked  AuditVisibility = "masked"
)

// AuditPolicy 是信封审计展示策略。
type AuditPolicy struct {
	Visibility AuditVisibility `json:"visibility"`
	Label      string          `json:"label,omitempty"`
}

// Policy 承接数据留存、认证、审计与脱敏策略。
type Policy struct {
	DataRetention        DataRetentionNode `json:"data_retention"`
	Auth                 AuthPolicy        `json:"auth"`
	Audit                AuditPolicy       `json:"audit"`
	Redaction            RedactionClass    `json:"redaction"`
	ReleaseGateThreshold map[string]string `json:"release_gate_threshold,omitempty"`
}

// StreamMode 声明信封的传输形态。
type StreamMode string

const (
	StreamModeBuffered  StreamMode = "buffered"
	StreamModeStreaming StreamMode = "streaming"
	StreamModeReplay    StreamMode = "replay"
)

// FallbackBoundary 表达可安全切换上游的边界。
type FallbackBoundary string

const (
	FallbackBeforeFirstByte       FallbackBoundary = "before_first_byte"
	FallbackAfterFirstByteBlocked FallbackBoundary = "after_first_byte_blocked"
	FallbackAfterFirstByteAllowed FallbackBoundary = "after_first_byte_allowed"
)

// MidStreamFallbackPolicy 是首字节后失败的恢复策略。
type MidStreamFallbackPolicy string

const (
	MidStreamFallbackNone         MidStreamFallbackPolicy = "none"
	MidStreamFallbackContinuation MidStreamFallbackPolicy = "continuation"
	MidStreamFallbackRestart      MidStreamFallbackPolicy = "restart"
)

// StreamPlan 描述事件输出、结束条件与失败恢复边界。
type StreamPlan struct {
	Mode                     StreamMode              `json:"mode"`
	EventClasses             []string                `json:"event_classes"`
	FlushPolicy              string                  `json:"flush_policy"`
	TerminalRequired         bool                    `json:"terminal_required"`
	SyntheticTerminalAllowed bool                    `json:"synthetic_terminal_allowed"`
	FallbackBoundary         FallbackBoundary        `json:"fallback_boundary"`
	MidStreamFallbackPolicy  MidStreamFallbackPolicy `json:"mid_stream_fallback_policy"`
	Recoverable              bool                    `json:"recoverable,omitempty"`
}
