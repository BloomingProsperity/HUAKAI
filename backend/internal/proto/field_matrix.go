// field_matrix.go — Upgrade #7 U7-E atomic：字段级 verdict matrix。
//
// 与 capability_matrix.go 的 FeatureName matrix 互补：
//   - CapabilityMatrix: feature-level (text_streaming / tool_use / 等粗粒度)
//     已存在，用于在 RequestToCanonical 阶段做能力路由
//   - FieldMatrix: field-level (system_fingerprint / cache_creation_input_tokens
//     / service_tier 等具体字段名) — 本文件，**仅作 audit / 运维可观测**
//     （不参与 passthrough 决策——adapter 当前无条件透传 envelope.Extra）
//
// PRESERVE-by-default 是核心升级语义：未在 matrix 显式登记的字段返回
// FieldPreservedDefault，**不**是 FieldUnsupported。这是 HUAKAI 区别于
// sub2api / new-api 的关键 — 后者 hardcode 已知字段，新字段必须改代码。
//
// 设计综合（claude lane + codex lane plan）：
//   - codex lane 建议：每条 entry 带 reason + 区分 lossy/lossless transform
//   - claude lane 建议：用嵌套 map 简化 key 路径
//   - 综合：FieldMatrixEntry 携带 metadata (Verdict + TransformKind + Reason)；
//     用嵌套 map 存；提供 Lookup（返回完整 entry）+ LookupVerdict（短路返回 verdict）
package proto

// FieldVerdict 是字段级别的处理裁定。
type FieldVerdict string

const (
	// FieldPreserved 显式登记保留（typed struct 已声明 + 完整 round-trip）。
	FieldPreserved FieldVerdict = "preserved"

	// FieldTransformed 显式登记做转换（如 stop_reason 翻译为 CanonicalStopReason）。
	FieldTransformed FieldVerdict = "transformed"

	// FieldDropped 显式登记主动丢弃（罕见 — 如已知 vendor 内部 debug 字段）。
	FieldDropped FieldVerdict = "dropped"

	// FieldPreservedDefault 未在 matrix 登记 → 默认保留。
	// 这是 HUAKAI 区别于 sub2api 的核心：vendor 加新字段不再丢失。
	FieldPreservedDefault FieldVerdict = "preserved_default"
)

// FieldTransformKind 标注 transform 的信息保真度。
type FieldTransformKind string

const (
	// FieldTransformNone 不适用（用于非 transform verdict）。
	FieldTransformNone FieldTransformKind = ""

	// FieldTransformLossless 转换无信息丢失（如 prompt_tokens → input_tokens 名变值不变）。
	FieldTransformLossless FieldTransformKind = "lossless"

	// FieldTransformLossy 转换有信息丢失（如 OpenAI finish_reason 多对一映射到
	// CanonicalStopReason，"length"/"stop"/"tool_calls" 之外都映射为 unknown）。
	FieldTransformLossy FieldTransformKind = "lossy"
)

// FieldMatrixEntry 是显式登记的字段 verdict + 元数据。
//
// 元数据用途：
//   - Reason: 运维查询时给出可读说明（"preserved via typed struct" /
//     "passthrough via PassthroughEnvelope" / "transformed lossy due to
//     enum mapping" 等）
//   - TransformKind: 仅当 Verdict == FieldTransformed 有意义；标 lossy/lossless
//     便于 compliance audit
type FieldMatrixEntry struct {
	Verdict       FieldVerdict
	TransformKind FieldTransformKind
	Reason        string
}

// FieldMatrix 是 (client_protocol, upstream_protocol, field_name) → entry
// 的查询表。未登记字段 Lookup 返回 verdict=FieldPreservedDefault 的 entry。
type FieldMatrix map[ClientProtocol]map[UpstreamProtocol]map[string]FieldMatrixEntry

// Lookup 查询 (client, upstream, fieldName) 对应的完整 FieldMatrixEntry。
// 未登记 = FieldPreservedDefault entry（不是 FieldUnsupported — PRESERVE-by-default）。
func (m FieldMatrix) Lookup(client ClientProtocol, upstream UpstreamProtocol, fieldName string) FieldMatrixEntry {
	if byUpstream, ok := m[client]; ok {
		if byField, ok := byUpstream[upstream]; ok {
			if entry, ok := byField[fieldName]; ok {
				return entry
			}
		}
	}
	return FieldMatrixEntry{
		Verdict: FieldPreservedDefault,
		Reason:  "unregistered field preserved by passthrough default",
	}
}

// LookupVerdict 是 Lookup 的短路简化版，只返回 verdict（不要 metadata 时用）。
func (m FieldMatrix) LookupVerdict(client ClientProtocol, upstream UpstreamProtocol, fieldName string) FieldVerdict {
	return m.Lookup(client, upstream, fieldName).Verdict
}

// HasEntry 报告 (client, upstream, fieldName) 是否在 matrix 中显式登记。
// 用于运维诊断：区分 "未登记 → 默认保留" 与 "已登记 = FieldPreserved"——
// 即便 verdict 相同，已登记意味着 typed struct / 转换路径已审过。
func (m FieldMatrix) HasEntry(client ClientProtocol, upstream UpstreamProtocol, fieldName string) bool {
	if byUpstream, ok := m[client]; ok {
		if byField, ok := byUpstream[upstream]; ok {
			_, has := byField[fieldName]
			return has
		}
	}
	return false
}

// 内部 helper：构造 FieldPreserved entry。
func preserved(reason string) FieldMatrixEntry {
	return FieldMatrixEntry{Verdict: FieldPreserved, Reason: reason}
}

// 内部 helper：构造 FieldTransformed entry，必带 TransformKind。
func transformed(kind FieldTransformKind, reason string) FieldMatrixEntry {
	return FieldMatrixEntry{Verdict: FieldTransformed, TransformKind: kind, Reason: reason}
}

// DefaultFieldMatrix 返回 HUAKAI 当前已知字段的 verdict 登记。
//
// 维护原则:
//   - 只登记 HUAKAI typed struct 已声明 + 行为已验证的字段（FieldPreserved
//     reason 注 "typed struct"）或 passthrough 路径已覆盖的字段（reason 注
//     "passthrough envelope"）
//   - 跨协议转换的字段登记为 FieldTransformed + lossy/lossless 标注
//   - 未登记 = 默认保留（U7-A/U7-D wire-up 已实现透传）
//   - 加新字段时：登记的目的是文档化 + 运维可查，不是限制透传
func DefaultFieldMatrix() FieldMatrix {
	return FieldMatrix{
		// =====================================================================
		// OpenAI 客户端 × OpenAI 上游
		// =====================================================================
		ClientProtocolOpenAIChat: {
			UpstreamProtocolOpenAI: {
				// HUAKAI typed struct 已声明（U7-C openAIChatCompletionChunk +
				// openAIChatCompletionResponse）
				"id":         preserved("typed struct: openAIChatCompletionChunk.ID"),
				"object":     preserved("typed struct: openAIChatCompletionChunk.Object"),
				"model":      preserved("typed struct: openAIChatCompletionChunk.Model"),
				"choices":    preserved("typed struct: openAIChatCompletionChunk.Choices"),
				"usage":      preserved("typed struct: openAIChatCompletionChunk.Usage"),
				"role":       preserved("typed struct: openAIStreamDelta.Role"),
				"content":    preserved("typed struct: openAIStreamDelta.Content"),
				"tool_calls": preserved("typed struct: openAIStreamDelta.ToolCalls"),
				"refusal":    preserved("typed struct: openAIStreamDelta.Refusal"),
				// transformed: stop_reason / finish_reason 翻译为 CanonicalStopReason
				// "length"/"stop"/"tool_calls"/"content_filter" 已知映射，其它
				// 落到 CanonicalStopUnknown — 多对一是 lossy
				"finish_reason": transformed(FieldTransformLossy,
					"OpenAI finish_reason 多对一映射到 CanonicalStopReason; 未知值落到 unknown"),
				// vendor 后加但典型客户端期望透传的字段（U7-A passthrough 自动覆盖；
				// 此处登记为运维文档）
				"system_fingerprint":    preserved("passthrough envelope: U7-C 自动透传"),
				"service_tier":          preserved("passthrough envelope: U7-C 自动透传"),
				"logprobs":              preserved("passthrough envelope: U7-C 自动透传"),
				"created":               preserved("passthrough envelope: U7-C 自动透传"),
				"prompt_filter_results": preserved("passthrough envelope: U7-C 自动透传 (Azure OpenAI)"),
			},
		},
		ClientProtocolOpenAIResponses: {
			UpstreamProtocolOpenAI: {
				"id":      preserved("typed struct: openAIChatCompletionResponse.ID"),
				"object":  preserved("typed struct: openAIChatCompletionResponse.Object"),
				"model":   preserved("typed struct: openAIChatCompletionResponse.Model"),
				"choices": preserved("typed struct: openAIChatCompletionResponse.Choices"),
				"usage":   preserved("typed struct: openAIChatCompletionResponse.Usage"),
			},
		},
		// =====================================================================
		// Anthropic 客户端 × Anthropic 上游
		// =====================================================================
		ClientProtocolAnthropicMessages: {
			UpstreamProtocolAnthropic: {
				// HUAKAI typed struct（U7-D anthropicEnvelope）
				"type":          preserved("typed struct: anthropicEnvelope.Type"),
				"message":       preserved("typed struct: anthropicEnvelope.Message"),
				"index":         preserved("typed struct: anthropicEnvelope.Index"),
				"content_block": preserved("typed struct: anthropicEnvelope.ContentBlock"),
				"delta":         preserved("typed struct: anthropicEnvelope.Delta"),
				"usage":         preserved("typed struct: anthropicEnvelope.Usage"),
				// transformed: stop_reason 翻译为 CanonicalStopReason
				// 实际是 Lossy——mapStopReason 有 default→CanonicalStopUnknown 分支
				// 触发 stopLoss VerdictLossy（参 proto/anthropic/sse.go）。
				// vendor 加新 stop_reason 值会落到 unknown，是 lossy 的本质。
				"stop_reason": transformed(FieldTransformLossy,
					"Anthropic stop_reason 多对一映射；未知值落到 CanonicalStopUnknown"),
				// Anthropic 已加但 HUAKAI 当前 typed 未抓的字段（U7-A passthrough）
				"cache_creation_input_tokens": preserved("passthrough envelope: U7-D 自动透传"),
				"cache_read_input_tokens":     preserved("passthrough envelope: U7-D 自动透传"),
				"service_tier":                preserved("passthrough envelope: U7-D 自动透传"),
			},
			// Bedrock-on-Anthropic 走同一 envelope 形态（A4 delegate）
			UpstreamProtocolBedrock: {
				"type":          preserved("typed struct: anthropicEnvelope.Type (via Bedrock delegate)"),
				"message":       preserved("typed struct: anthropicEnvelope.Message (via Bedrock delegate)"),
				"index":         preserved("typed struct: anthropicEnvelope.Index (via Bedrock delegate)"),
				"content_block": preserved("typed struct: anthropicEnvelope.ContentBlock (via Bedrock delegate)"),
				"delta":         preserved("typed struct: anthropicEnvelope.Delta (via Bedrock delegate)"),
				"usage":         preserved("typed struct: anthropicEnvelope.Usage (via Bedrock delegate)"),
				"stop_reason": transformed(FieldTransformLossy,
					"Bedrock-on-Anthropic 复用 anthropic.Adapter stop_reason 映射 (Lossy 同 Anthropic)"),
				"cache_creation_input_tokens": preserved("passthrough envelope via Bedrock delegate"),
				"cache_read_input_tokens":     preserved("passthrough envelope via Bedrock delegate"),
			},
		},
	}
}
