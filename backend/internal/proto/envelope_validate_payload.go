package proto

import (
	"encoding/json"
	"fmt"
	"strings"
)

func validateCapabilityNodeMeta(node CapabilityNode, idx int) error {
	if _, ok := streamReadinessSet[node.StreamReady]; !ok {
		return &ValidationError{
			Inv:     "INV-14",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].StreamReady=%q is not in StreamReadiness enum {yes,no,partial}", idx, node.StreamReady),
		}
	}
	return nil
}

// validateCapabilityNodePayload 按 Kind 分发到具体 payload validator。
//
// P-1 D1 覆盖 5 个 enum 字段；D2 起补 TextNode/Cache/Structured/ComputerUse/Batch 与必填 +
// JSON 形态守门；D3 起补跨 node ref；D4 起补条件必填 + 一致性。
// Kind 已守门，此处仅按命中分发，未命中（未来 Kind 新增）静默通过留给该校验报错。
func validateCapabilityNodePayload(node CapabilityNode, idx int) error {
	switch node.Kind {
	case CapabilityText:
		return validateTextPayload(node.Text, idx)
	case CapabilityToolUse:
		return validateToolUsePayload(node.ToolUse, idx)
	case CapabilityToolResult:
		return validateToolResultPayload(node.ToolResult, idx)
	case CapabilityThinking:
		return validateThinkingPayload(node.Thinking, idx)
	case CapabilityCacheControl:
		return validateCachePayload(node.CacheControl, idx)
	case CapabilityStructuredOutput:
		return validateStructuredPayload(node.StructuredOutput, idx)
	case CapabilityComputerUse:
		return validateComputerUsePayload(node.ComputerUse, idx)
	case CapabilityAudio:
		return validateAudioPayload(node.Audio, idx)
	case CapabilityVideo:
		return validateVideoPayload(node.Video, idx)
	case CapabilityLiveSession:
		return validateLiveSessionPayload(node.LiveSession, idx)
	case CapabilityBatch:
		return validateBatchPayload(node.Batch, idx)
	case CapabilityDataRetention:
		return validateDataRetentionPayload(node.DataRetention, idx)
	}
	return nil
}

// validateTextPayload 校验 TextNode.Role + Block.Type。
func validateTextPayload(payload *TextNode, idx int) error {
	if payload == nil {
		return nil
	}
	if _, ok := textRoleSet[payload.Role]; !ok {
		return &ValidationError{
			Inv:     "INV-15",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].text.role=%q is not in TextRole enum {user,assistant,system,tool}", idx, payload.Role),
		}
	}
	if payload.Block.Type != "text" {
		return &ValidationError{
			Inv:     "INV-15",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].text.block.type=%q must be %q", idx, payload.Block.Type, "text"),
		}
	}
	return nil
}

// validateToolUsePayload 校验 ToolUse payload enum、必填与 partial input 规则。
//
//   - enum：Status 必须在 ToolNodeStatus enum 内
//   - 必填：ToolCallID/Name 非空 + Input 必须是 JSON object 或 null
//   - PartialInput 仅在 Status ∈ {pending, partial} 时允许非空
func validateToolUsePayload(payload *ToolUseNode, idx int) error {
	if payload == nil {
		return nil
	}
	if _, ok := toolUseStatusSet[payload.Status]; !ok {
		return &ValidationError{
			Inv:     "INV-16",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].tool_use.status=%q is not in ToolNodeStatus enum {pending,partial,complete,error}", idx, payload.Status),
		}
	}
	if payload.ToolCallID == "" {
		return &ValidationError{
			Inv:     "INV-16",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].tool_use.tool_call_id is required", idx),
		}
	}
	if payload.Name == "" {
		return &ValidationError{
			Inv:     "INV-16",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].tool_use.name is required", idx),
		}
	}
	if !isJSONObjectOrNull(payload.Input) {
		return &ValidationError{
			Inv:     "INV-16",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].tool_use.input must be a JSON object or null (got %s)", idx, jsonHintForError(payload.Input)),
		}
	}
	if hasJSONContent(payload.PartialInput) {
		switch payload.Status {
		case ToolNodePending, ToolNodePartial:
			// 合法：streaming 累积期的 partial delta
		default:
			return &ValidationError{
				Inv:     "INV-17",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].tool_use.partial_input is only allowed when status is pending or partial (got status=%q)", idx, payload.Status),
			}
		}
	}
	return nil
}

// validateToolResultPayload 校验 ToolResult payload enum、必填与错误状态一致性。
//
//   - enum：Status 必须在 {complete, error} 内（pending/partial 在 ToolResult 上语义不成立）
//   - 必填：ToolCallID 非空 + Content slice 非 nil（空数组合法，nil 不合法）
//   - 一致性：IsError ↔ Status == error
func validateToolResultPayload(payload *ToolResultNode, idx int) error {
	if payload == nil {
		return nil
	}
	if _, ok := toolResultStatusSet[payload.Status]; !ok {
		return &ValidationError{
			Inv:     "INV-18",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].tool_result.status=%q is not in ToolResult status enum {complete,error}", idx, payload.Status),
		}
	}
	if payload.ToolCallID == "" {
		return &ValidationError{
			Inv:     "INV-18",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].tool_result.tool_call_id is required", idx),
		}
	}
	if payload.Content == nil {
		return &ValidationError{
			Inv:     "INV-18",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].tool_result.content must not be nil (empty array is allowed)", idx),
		}
	}
	if (payload.Status == ToolNodeError) != payload.IsError {
		return &ValidationError{
			Inv:     "INV-18",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].tool_result is_error=%v is inconsistent with status=%q (must be error ↔ true)", idx, payload.IsError, payload.Status),
		}
	}
	return nil
}

// validateThinkingPayload 校验 Thinking payload enum、非负数值与隐藏文本一致性。
//
//   - enum：Redaction 必须在 RedactionClass enum 内
//   - 数值非负：BudgetTokens/HiddenTokens >= 0
//   - Redaction ∈ {hidden, provider_only} 时 Blocks 不得含 type=text 且 text 非空的块
//     （隐藏意图 + 可见文本 = 矛盾态，issue-mode "redaction bypass via blocks"）
func validateThinkingPayload(payload *ThinkingNode, idx int) error {
	if payload == nil {
		return nil
	}
	if _, ok := redactionClassSet[payload.Redaction]; !ok {
		return &ValidationError{
			Inv:     "INV-39",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].thinking.redaction=%q is not in RedactionClass enum {public,redacted,hidden,provider_only}", idx, payload.Redaction),
		}
	}
	if payload.BudgetTokens < 0 {
		return &ValidationError{
			Inv:     "INV-39",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].thinking.budget_tokens=%d must be >= 0", idx, payload.BudgetTokens),
		}
	}
	if payload.HiddenTokens < 0 {
		return &ValidationError{
			Inv:     "INV-39",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].thinking.hidden_tokens=%d must be >= 0", idx, payload.HiddenTokens),
		}
	}
	if payload.Redaction == RedactionHidden || payload.Redaction == RedactionProviderOnly {
		for j, b := range payload.Blocks {
			if b.Type == "text" && b.Text != "" {
				return &ValidationError{
					Inv:     "INV-40",
					Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].thinking.blocks[%d] visible text not allowed when redaction=%q", idx, j, payload.Redaction),
				}
			}
		}
	}
	return nil
}

// validateDataRetentionPayload 校验 DataRetention 条件必填。
//
//   - Value=request_store_false → RequestStore 非 nil 且 *RequestStore == false
//   - Value=regional_asserted → Region 非空；Value=zdr_verified → EvidenceRef + Enforcement="verified"
//   - Value=provider_contract_required → Enforcement="contract_required" + EvidenceRef
//
// Value enum 本身的守门由 policy 层和 payload 内 check 共同覆盖。
func validateDataRetentionPayload(payload *DataRetentionNode, idx int) error {
	if payload == nil {
		return nil
	}
	// Value enum 守门共用 AllDataRetentionLabels。
	found := false
	for _, v := range AllDataRetentionLabels {
		if payload.Value == v {
			found = true
			break
		}
	}
	if !found {
		return &ValidationError{
			Inv:     "INV-30",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].data_retention.value=%q not in 5-vocab enum", idx, payload.Value),
		}
	}
	if payload.AuditLabel == "" {
		return &ValidationError{
			Inv:     "INV-30",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].data_retention.audit_label is required", idx),
		}
	}
	switch payload.Value {
	case DataRetentionRequestStoreFalse:
		if payload.RequestStore == nil || *payload.RequestStore {
			return &ValidationError{
				Inv:     "INV-30",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].data_retention value=request_store_false requires explicit request_store=false", idx),
			}
		}
	case DataRetentionRegionalAsserted:
		if payload.Region == "" {
			return &ValidationError{
				Inv:     "INV-31",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].data_retention value=regional_asserted requires region", idx),
			}
		}
	case DataRetentionZDRVerified:
		if payload.EvidenceRef == "" {
			return &ValidationError{
				Inv:     "INV-31",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].data_retention value=zdr_verified requires evidence_ref", idx),
			}
		}
		if payload.Enforcement != "verified" {
			return &ValidationError{
				Inv:     "INV-31",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].data_retention value=zdr_verified requires enforcement=verified (got %q)", idx, payload.Enforcement),
			}
		}
	case DataRetentionProviderContractRequired:
		if payload.Enforcement != "contract_required" {
			return &ValidationError{
				Inv:     "INV-32",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].data_retention value=provider_contract_required requires enforcement=contract_required (got %q)", idx, payload.Enforcement),
			}
		}
		if payload.EvidenceRef == "" {
			return &ValidationError{
				Inv:     "INV-32",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].data_retention value=provider_contract_required requires evidence_ref", idx),
			}
		}
	}
	return nil
}

// validateCachePayload 校验 CacheControl.Scope enum + LocalityHint 白名单。
func validateCachePayload(payload *CacheControlNode, idx int) error {
	if payload == nil {
		return nil
	}
	if _, ok := cacheScopeSet[payload.Scope]; !ok {
		return &ValidationError{
			Inv:     "INV-25",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].cache_control.scope=%q is not in CacheScope enum {request,message,block,session,vendor}", idx, payload.Scope),
		}
	}
	if payload.LocalityHint != "" {
		if _, ok := cacheLocalityHintSet[payload.LocalityHint]; !ok {
			return &ValidationError{
				Inv:     "INV-25",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].cache_control.locality_hint=%q is not in PASR locality whitelist {account_pin,account_recent,global}", idx, payload.LocalityHint),
			}
		}
	}
	return nil
}

// validateStructuredPayload 校验 Mode enum 与 json_schema schema 形态。
func validateStructuredPayload(payload *StructuredOutputNode, idx int) error {
	if payload == nil {
		return nil
	}
	if _, ok := structuredOutputModeSet[payload.Mode]; !ok {
		return &ValidationError{
			Inv:     "INV-36",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].structured_output.mode=%q is not in StructuredOutputMode enum {json_mode,json_schema,tool_strategy,provider_native}", idx, payload.Mode),
		}
	}
	if payload.Mode == StructuredOutputJSONSchema {
		if !isJSONObject(payload.Schema) {
			return &ValidationError{
				Inv:     "INV-37",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].structured_output.schema must be a JSON object when mode=json_schema (got %s)", idx, jsonHintForError(payload.Schema)),
			}
		}
	}
	return nil
}

// validateComputerUsePayload 校验 Environment 白名单 + Action 必填 + Approval enum。
func validateComputerUsePayload(payload *ComputerUseNode, idx int) error {
	if payload == nil {
		return nil
	}
	if _, ok := computerEnvironmentSet[payload.Environment]; !ok {
		return &ValidationError{
			Inv:     "INV-34",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].computer_use.environment=%q is not in ComputerEnvironment whitelist {browser,desktop,shell,mobile,other}", idx, payload.Environment),
		}
	}
	if payload.Action == "" {
		return &ValidationError{
			Inv:     "INV-34",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].computer_use.action is required", idx),
		}
	}
	if _, ok := approvalStateSet[payload.Approval]; !ok {
		return &ValidationError{
			Inv:     "INV-34",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].computer_use.approval=%q is not in ApprovalState enum {required,granted,denied,not_required}", idx, payload.Approval),
		}
	}
	return nil
}

// validateBatchPayload 校验必填字段、Validation enum 与 RetryPolicy 白名单。
func validateBatchPayload(payload *BatchNode, idx int) error {
	if payload == nil {
		return nil
	}
	if payload.JobID == "" {
		return &ValidationError{
			Inv:     "INV-27",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].batch.job_id is required", idx),
		}
	}
	if payload.Endpoint == "" {
		return &ValidationError{
			Inv:     "INV-27",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].batch.endpoint is required", idx),
		}
	}
	if payload.InputRef == "" {
		return &ValidationError{
			Inv:     "INV-27",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].batch.input_ref is required", idx),
		}
	}
	if _, ok := batchStatusSet[payload.Validation]; !ok {
		return &ValidationError{
			Inv:     "INV-27",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].batch.validation=%q is not in BatchStatus enum {pending,validated,failed,complete}", idx, payload.Validation),
		}
	}
	if payload.RetryPolicy != nil {
		if payload.RetryPolicy.MaxAttempts < 0 {
			return &ValidationError{
				Inv:     "INV-29",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].batch.retry_policy.max_attempts=%d must be >= 0", idx, payload.RetryPolicy.MaxAttempts),
			}
		}
		if payload.RetryPolicy.Backoff != "" {
			if _, ok := retryBackoffSet[payload.RetryPolicy.Backoff]; !ok {
				return &ValidationError{
					Inv:     "INV-29",
					Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].batch.retry_policy.backoff=%q is not in RetryBackoff whitelist {fixed,exponential,provider_default}", idx, payload.RetryPolicy.Backoff),
				}
			}
		}
	}
	return nil
}

// validateAudioPayload 校验 Audio.Transport enum 与 Format 白名单。
//
//   - Transport ∈ {inline, file, url, stream}
//   - Format 必须在 P-1 白名单 {wav,mp3,opus,pcm16,flac,m4a,webm}（只增不删）
//
// Transport↔Locator.Kind 映射推迟到 P-2 ClientAdapter（届时有真 provider 上下文可对齐）。
func validateAudioPayload(payload *AudioNode, idx int) error {
	if payload == nil {
		return nil
	}
	if _, ok := mediaTransportSet[payload.Transport]; !ok {
		return &ValidationError{
			Inv:     "INV-23",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].audio.transport=%q is not in MediaTransport enum {inline,file,url,stream}", idx, payload.Transport),
		}
	}
	if payload.Format == "" {
		return &ValidationError{
			Inv:     "INV-23",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].audio.format is required", idx),
		}
	}
	if _, ok := audioFormatSet[payload.Format]; !ok {
		return &ValidationError{
			Inv:     "INV-23",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].audio.format=%q is not in P-1 whitelist {wav,mp3,opus,pcm16,flac,m4a,webm}", idx, payload.Format),
		}
	}
	return nil
}

// validateVideoPayload 校验 VideoNode.TimeRange 单调。
//
//   - StartMillis / EndMillis 都必须 >= 0
//   - EndMillis > 0 时（0 表示到结尾 / 未知）必须 >= StartMillis
//
// MediaType / Locator / Codec 等的守门推迟到 P-2 ClientAdapter（媒体协议层）。
func validateVideoPayload(payload *VideoNode, idx int) error {
	if payload == nil || payload.TimeRange == nil {
		return nil
	}
	tr := payload.TimeRange
	if tr.StartMillis < 0 {
		return &ValidationError{
			Inv:     "INV-49",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].video.time_range.start_ms=%d must be >= 0", idx, tr.StartMillis),
		}
	}
	if tr.EndMillis < 0 {
		return &ValidationError{
			Inv:     "INV-49",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].video.time_range.end_ms=%d must be >= 0", idx, tr.EndMillis),
		}
	}
	if tr.EndMillis > 0 && tr.EndMillis < tr.StartMillis {
		return &ValidationError{
			Inv:     "INV-49",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].video.time_range end_ms=%d < start_ms=%d", idx, tr.EndMillis, tr.StartMillis),
		}
	}
	return nil
}

// validateLiveSessionPayload 校验 Live.Transport enum。
func validateLiveSessionPayload(payload *LiveSessionNode, idx int) error {
	if payload == nil {
		return nil
	}
	if _, ok := liveTransportSet[payload.Transport]; !ok {
		return &ValidationError{
			Inv:     "INV-41",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].live_session.transport=%q is not in LiveTransport enum {wss,sse}", idx, payload.Transport),
		}
	}
	return nil
}

// isJSONObjectOrNull 判定 raw 是否是合法 JSON object（{...}）或 JSON null。
//
// 用于校验（ToolUse.Input 必须 JSON object 或 null）。空切片视为缺值，返回 false；
// 调用方需在前置必填检查里覆盖"缺字段"的情形。
func isJSONObjectOrNull(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	if !json.Valid(raw) {
		return false
	}
	s := strings.TrimSpace(string(raw))
	if s == "null" {
		return true
	}
	return len(s) > 0 && s[0] == '{'
}

// isJSONObject 判定 raw 是否是合法 JSON object（不含 null）。用于校验 json_schema 模式。
func isJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	if !json.Valid(raw) {
		return false
	}
	s := strings.TrimSpace(string(raw))
	return len(s) > 0 && s[0] == '{'
}

// hasJSONContent 判定 raw 是否携带语义内容（非空、非纯空白、非 JSON null）。
//
// 用于校验：PartialInput 是否被 caller 显式设置；JSON null 视为"明示无值"，等价未设置。
func hasJSONContent(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null"
}

// jsonHintForError 给错误消息生成简短的 JSON 形态提示（截断、去空白），用于校验错误提示。
func jsonHintForError(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "<empty>"
	}
	s := strings.TrimSpace(string(raw))
	if len(s) > 64 {
		s = s[:64] + "..."
	}
	return s
}
