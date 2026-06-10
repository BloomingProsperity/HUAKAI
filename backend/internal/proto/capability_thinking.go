package proto

// RedactionClass 标记 reasoning/thinking 内容的可见性策略。
type RedactionClass string

const (
	RedactionPublic       RedactionClass = "public"
	RedactionRedacted     RedactionClass = "redacted"
	RedactionHidden       RedactionClass = "hidden"
	RedactionProviderOnly RedactionClass = "provider_only"
)

// ThinkingNode 是 thinking capability 的 payload；表达 visible/hidden reasoning。
type ThinkingNode struct {
	// Mode 记录 thinking 形态："enabled"(手动 budget_tokens)或 "adaptive"
	// (always-on，无 budget_tokens，如 claude-fable-5 / opus-4.7+)。空=兼容旧行为
	// 按 enabled 处理；adaptive 模式 BudgetTokens 通常为 0,合法不应被当作丢弃信号。
	Mode string `json:"mode,omitempty"`

	// BudgetTokens 必填；0 表示 provider 未声明 budget。
	BudgetTokens int `json:"budget_tokens"`

	// Blocks 必填；可为空数组；visible thinking 内容用 CanonicalContentBlock 表达。
	Blocks []CanonicalContentBlock `json:"blocks"`

	// HiddenTokens 可选；provider 报告但不可见的 reasoning token。
	HiddenTokens int `json:"hidden_tokens,omitempty"`

	// Signature 可选；Anthropic signature_delta 等策略受控字段。
	Signature string `json:"signature,omitempty"`

	// Redaction 必填；public/redacted/hidden/provider_only。
	Redaction RedactionClass `json:"redaction"`
}
