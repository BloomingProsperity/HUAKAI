package proto

import "encoding/json"

// StructuredOutputMode 是结构化输出模式。
type StructuredOutputMode string

const (
	StructuredOutputJSONMode       StructuredOutputMode = "json_mode"
	StructuredOutputJSONSchema     StructuredOutputMode = "json_schema"
	StructuredOutputToolStrategy   StructuredOutputMode = "tool_strategy"
	StructuredOutputProviderNative StructuredOutputMode = "provider_native"
)

// StructuredOutputNode 是 structured_output capability 的 payload。
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
