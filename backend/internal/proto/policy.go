package proto

// AuthPolicy 是 envelope 的认证策略；D5 已批 standard + audit。
type AuthPolicy string

const (
	// AuthPolicyStandard 标准入口认证（同 /v1/chat/completions 主路由）。D5 默认。
	AuthPolicyStandard AuthPolicy = "standard"

	// AuthPolicyRevoked 凭据失效；envelope 标记后由 forwarder 拒绝。
	AuthPolicyRevoked AuthPolicy = "revoked"
)

// AuditVisibility 控制 envelope 在 admin / observability surface 的可见度。
type AuditVisibility string

const (
	// AuditVisible 完整可见。
	AuditVisible AuditVisibility = "visible"

	// AuditMasked 字段遮蔽（如 prompt 内容打码）。
	AuditMasked AuditVisibility = "masked"
)

// AuditPolicy 是 envelope 审计策略。
type AuditPolicy struct {
	// Visibility 必填；visible/masked。
	Visibility AuditVisibility `json:"visibility"`

	// Label 可选；审计标签（如 standard / native_passthrough / regression_fixture）。
	Label string `json:"label,omitempty"`
}

// Redaction 复用 capability_thinking.go 中已有 RedactionClass 枚举，避免重复定义。
// RedactionClass 取值：public / redacted / hidden / provider_only。

// Policy 承接 envelope 的 data_retention / 认证 / 审计 / redaction 策略。
type Policy struct {
	// DataRetention 必填；锁定 D12 5 词汇。
	DataRetention DataRetentionNode `json:"data_retention"`

	// Auth 必填；D5 默认 standard。
	Auth AuthPolicy `json:"auth"`

	// Audit 必填；可见度 + 标签。
	Audit AuditPolicy `json:"audit"`

	// Redaction 必填；P-0 默认 RedactionPublic；复用 RedactionClass。
	Redaction RedactionClass `json:"redaction"`

	// ReleaseGateThreshold 可选；D13 推迟决策点 schema 留位。
	// P-5 release gate 实施时填写阈值条目，P-0 留空。
	ReleaseGateThreshold map[string]string `json:"release_gate_threshold,omitempty"`
}
