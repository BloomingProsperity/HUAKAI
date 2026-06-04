package proto

// Accounting 承接 envelope 的计费 / 用量 / 证据标签；P-0 不做真实定价。
//
// 复用 CanonicalUsage 的 token 字段（input/output/cache_creation/cache_read），
// 在此基础上补 reasoning / live / batch 等异质 usage 信号。
type Accounting struct {
	// Usage 必填；token 维度计量，复用现有 CanonicalUsage。
	Usage CanonicalUsage `json:"usage,omitempty"`

	// UsageSource 可选；D8 推迟决策点 schema 留位；mock/smoke/real。
	// P-0 默认空（等价 mock 隐含约定），P-7 仪表板时再写入。
	UsageSource string `json:"usage_source,omitempty"`

	// ReasoningTokens 可选；OpenAI o1/o3 reasoning_tokens 与 Anthropic thinking 内部 token。
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`

	// LiveUsage 可选；live_session capability 关联用量。
	LiveUsage *LiveUsage `json:"live_usage,omitempty"`

	// BatchUsage 可选；batch capability 关联用量。
	BatchUsage *BatchUsage `json:"batch_usage,omitempty"`

	// EvidenceLabel 可选；与 RequestMeta.EvidenceLabel 同语义；空值等价 mock。
	EvidenceLabel EvidenceLabel `json:"evidence_label,omitempty"`

	// HopChain 可选；HUAKAI 信任链 T1+：记录 request → router → pool → account →
	// provider → response 的每跳证据；T2 由 forwarder 写入。
	// 详见 internal/proto/trust_chain_types.go HopAttestation。
	HopChain []HopAttestation `json:"hop_chain,omitempty"`

	// ModelChain 可选；HUAKAI 信任链 T3：三方比对 model 字段防商家偷换。
	// 详见 internal/proto/trust_chain_types.go ModelChain。
	ModelChain *ModelChain `json:"model_chain,omitempty"`

	// LedgerID 可选；HUAKAI 信任链 T4：引用 audit_ledger 表的行 ID；T4 才写入。
	LedgerID string `json:"ledger_id,omitempty"`

	// Signature 可选；HUAKAI 信任链 T2：ed25519 签名（base64）；T2 由 settler 写入。
	Signature string `json:"signature,omitempty"`

	// PubkeyFingerprint 可选；HUAKAI 信任链 T2：sha256(pubkey)[:16] hex；
	// Signature 非空时本字段必须非空（T2 落地时启用守门）。
	PubkeyFingerprint string `json:"pubkey_fp,omitempty"`
}

// LiveUsage 是 live_session capability 的 usage 子结构。
type LiveUsage struct {
	// SessionDurationMS 可选；会话总时长（毫秒）。
	SessionDurationMS int64 `json:"session_duration_ms,omitempty"`

	// InputAudioMS 可选；客户端推入音频时长。
	InputAudioMS int64 `json:"input_audio_ms,omitempty"`

	// OutputAudioMS 可选；服务端输出音频时长。
	OutputAudioMS int64 `json:"output_audio_ms,omitempty"`
}

// BatchUsage 是 batch capability 的 usage 子结构。
type BatchUsage struct {
	// Inputs 可选；批输入条目数。
	Inputs int `json:"inputs,omitempty"`

	// Outputs 可选；批输出条目数。
	Outputs int `json:"outputs,omitempty"`

	// Errors 可选；批错误条目数。
	Errors int `json:"errors,omitempty"`
}
