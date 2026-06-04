package proto

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ValidationError 描述一次 envelope 校验失败；包含违反的校验编号便于定位。
type ValidationError struct {
	Inv     string
	Message string // 人读说明
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("envelope %s: %s", e.Inv, e.Message)
}

// ValidateEnvelope 对 HCSFEnvelope 做所有结构性/语义性校验。
//
//   - marshal/unmarshal round-trip 不变量由测试覆盖
//   - nil/empty slice 等价由 encoding/json + omitempty 自然满足
//   - tagged-union 一致性
//   - Version=="0.4"
//   - RequestMeta 必填字段非空
//   - BufferedResponse + StreamEvents 至多一个非 nil
//   - ProtocolLoss 不可作为 silent drop（含 node/edge/projection 三层）
//   - Edge 必填 ID/Type/From/To；Type 必须在 AllEdgeTypes 中；From/To 必须命中 Nodes
//   - EdgeMutuallyExclusive 不可双向
//   - DataRetentionLabel 严格枚举
//   - MidStreamFallbackPolicy 默认 none
//   - Extensions key 前缀
//   - StreamPlan.Mode 必填且必须在 StreamMode 枚举内
//
// Payload enum、必填与复合一致性：
//
//   - CapabilityNode.StreamReady ∈ {yes, no, partial}
//   - TextNode.Role ∈ {user, assistant, system, tool}; Block.Type == "text"
//   - ToolUseNode：Status enum + ToolCallID/Name 非空 + Input 必须 JSON object 或 null
//   - ToolUseNode.PartialInput 仅在 Status ∈ {pending, partial} 时允许出现
//   - ToolResultNode：Status ∈ {complete, error} + ToolCallID 非空 + Content 非 nil + IsError ↔ Status==error
//   - AudioNode.Transport ∈ {inline, file, url, stream}（仅 D1 enum 部分；D2 补 Format 白名单；D3 补 Transport↔Locator 映射）
//   - CacheControlNode：Scope ∈ {request, message, block, session, vendor}；LocalityHint 非空时 ∈ {account_pin, account_recent, global}
//   - BatchNode：JobID/Endpoint/InputRef 非空 + Validation ∈ {pending, validated, failed, complete}
//   - RetryPolicy：MaxAttempts >= 0 + Backoff 非空时 ∈ {fixed, exponential, provider_default}
//   - ComputerUseNode：Environment ∈ {browser, desktop, shell, mobile, other} + Action 非空 + Approval ∈ {required, granted, denied, not_required}
//   - StructuredOutputNode.Mode ∈ {json_mode, json_schema, tool_strategy, provider_native}
//   - StructuredOutputNode.Mode == json_schema 时 Schema 必须是 JSON object（D2 部分；D4 补 provider_native 关联）
//   - ThinkingNode：Redaction ∈ {public, redacted, hidden, provider_only} + BudgetTokens/HiddenTokens >= 0
//   - LiveSessionNode.Transport ∈ {wss, sse} + Modalities ⊂ {text, audio, video}（D5 补 ToolNodeIDs 引用解析）
//
// 跨 node/projection 引用完整性：
//
//   - CacheControl.BreakpointRefs 解析 + Scope=block/message 时非空
//   - ComputerUse.ScreenshotRef 解析到 image/file
//   - MCPServer.ServerLabel 必填（D5 补 InvocationNodeIDs/ResultNodeIDs 引用解析）
//   - ProviderProjection.CapabilityResults[i].NodeID 解析 + Capability == node.Kind
//   - NodeSourceRef MessageIndex/BlockIndex/EventIndex 非负 + 范围内
//
// 条件必填与一致性：
//
//   - DataRetention.Value=request_store_false 必须 RequestStore != nil 且 *RequestStore == false
//   - DataRetention.Value=regional_asserted → Region 非空；Value=zdr_verified → EvidenceRef + Enforcement=verified
//   - DataRetention.Value=provider_contract_required → Enforcement=contract_required + EvidenceRef
//   - graph DataRetentionNode 与 Policy.DataRetention 一致（P-1 仅 0/1 graph node）
//   - ThinkingNode.Redaction ∈ {hidden, provider_only} 时 Blocks 不得含 type=text 且 text 非空的块
//   - ProtocolLossEntry.Severity 非空时 ∈ enum；NodeID 非空时 resolve；Capability 非空时合法
//
// 剩余 cross-ref 与枚举检查：
//
//   - ToolResult.ToolCallID 必须匹配同图 ToolUse + 存在 requires edge（cross_ref.go）
//   - AudioNode.Format 白名单 {wav,mp3,opus,pcm16,flac,m4a,webm}
//   - BatchNode.InputRef/OutputRef/ErrorRef 非空时必须解析到同图 FileNode（cross_ref.go）
//   - LiveSession.ToolNodeIDs 必须解析到 tool_use/computer_use/mcp_server node（cross_ref.go）
//   - MCPServer.InvocationNodeIDs/ResultNodeIDs 必须解析（cross_ref.go）
//   - VideoNode.TimeRange 单调：StartMillis/EndMillis >= 0 且 EndMillis > 0 时 EndMillis >= StartMillis
//
func ValidateEnvelope(env *HCSFEnvelope) error {
	if env == nil {
		return &ValidationError{Inv: "INV-0", Message: "envelope is nil"}
	}

	if err := validateVersion(env); err != nil {
		return err
	}
	if err := validateRequestMeta(&env.RequestMeta); err != nil {
		return err
	}
	if err := validateEnvelopeShape(env); err != nil {
		return err
	}
	if err := validateCapabilityGraph(&env.CapabilityGraph); err != nil {
		return err
	}
	if err := validateProviderProjection(&env.ProviderProjection); err != nil {
		return err
	}
	if err := validateStreamPlan(&env.StreamPlan); err != nil {
		return err
	}
	if err := validatePolicy(&env.Policy); err != nil {
		return err
	}
	if err := validateExtensions(env.Extensions); err != nil {
		return err
	}
	if err := validateCrossRefs(env); err != nil {
		return err
	}
	return nil
}

func validateVersion(env *HCSFEnvelope) error {
	if env.Version != HCSFVersion {
		return &ValidationError{
			Inv:     "INV-4",
			Message: fmt.Sprintf("Version must be %q, got %q", HCSFVersion, env.Version),
		}
	}
	return nil
}

func validateRequestMeta(m *RequestMeta) error {
	if m.RequestID == "" {
		return &ValidationError{Inv: "INV-5", Message: "RequestMeta.RequestID is required"}
	}
	if m.ClientProtocol == "" {
		return &ValidationError{Inv: "INV-5", Message: "RequestMeta.ClientProtocol is required"}
	}
	if m.ProtocolFamily == "" {
		return &ValidationError{Inv: "INV-5", Message: "RequestMeta.ProtocolFamily is required"}
	}
	if m.Model == "" {
		return &ValidationError{Inv: "INV-5", Message: "RequestMeta.Model is required"}
	}
	if m.IngressPath == "" {
		return &ValidationError{Inv: "INV-5", Message: "RequestMeta.IngressPath is required"}
	}
	return nil
}

// validateEnvelopeShape 校验 BufferedResponse 与 StreamEvents 互斥关系。
//
// 形态推导规则（envelope.go:10-17）：StreamEvents non-nil 即视为 replay shape，包括
// `[]CanonicalEvent{}` 显式空切片。BufferedResponse + StreamEvents:nil 合法；
// BufferedResponse + StreamEvents:[] 合法（用户显式声明 buffered 形态、StreamEvents 不参与）；
// BufferedResponse + StreamEvents:[event{...}] 违反 shape 约束。
//
// 之前实现用 `len(StreamEvents) > 0` 与"non-nil 即 replay"语义不一致；现在改为只在
// StreamEvents 至少包含一个事件时才判定冲突，让"显式空切片"侧路也保持稳定 round-trip。
func validateEnvelopeShape(env *HCSFEnvelope) error {
	hasBuffered := env.BufferedResponse != nil
	hasStreamEvents := len(env.StreamEvents) > 0
	if hasBuffered && hasStreamEvents {
		return &ValidationError{
			Inv:     "INV-6",
			Message: "BufferedResponse and StreamEvents cannot both carry payload (StreamEvents has events)",
		}
	}
	return nil
}

func validateCapabilityGraph(g *CapabilityGraph) error {
	nodeIDs := make(map[string]CapabilityKind, len(g.Nodes))

	for i, node := range g.Nodes {
		if node.ID == "" {
			return &ValidationError{
				Inv:     "INV-8",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].ID is required", i),
			}
		}
		if _, dup := nodeIDs[node.ID]; dup {
			return &ValidationError{
				Inv:     "INV-8",
				Message: fmt.Sprintf("CapabilityGraph.Nodes duplicate ID %q", node.ID),
			}
		}
		nodeIDs[node.ID] = node.Kind

		if err := validateNodeTaggedUnion(node, i); err != nil {
			return err
		}
		if err := validateCapabilityNodeMeta(node, i); err != nil {
			return err
		}
		if err := validateCapabilityNodePayload(node, i); err != nil {
			return err
		}
		for j, loss := range node.ProtocolLoss {
			if loss.IsSilentDrop() {
				return &ValidationError{
					Inv:     "INV-7",
					Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].ProtocolLoss[%d] is a silent drop (no Reason/Note/Verdict/Code)", i, j),
				}
			}
		}
	}

	mutexPairs := make(map[edgePair]struct{}, len(g.Edges))
	edgeIDs := make(map[string]struct{}, len(g.Edges))
	for i, edge := range g.Edges {
		if edge.ID == "" {
			return &ValidationError{
				Inv:     "INV-8",
				Message: fmt.Sprintf("CapabilityGraph.Edges[%d].ID is required", i),
			}
		}
		if _, dup := edgeIDs[edge.ID]; dup {
			return &ValidationError{
				Inv:     "INV-8",
				Message: fmt.Sprintf("CapabilityGraph.Edges duplicate ID %q", edge.ID),
			}
		}
		edgeIDs[edge.ID] = struct{}{}

		if edge.Type == "" {
			return &ValidationError{
				Inv:     "INV-8",
				Message: fmt.Sprintf("CapabilityGraph.Edges[%d].Type is required", i),
			}
		}
		if _, ok := edgeTypeSet[edge.Type]; !ok {
			return &ValidationError{
				Inv:     "INV-8",
				Message: fmt.Sprintf("CapabilityGraph.Edges[%d].Type=%q is not in AllEdgeTypes enum", i, edge.Type),
			}
		}

		if edge.From == "" || edge.To == "" {
			return &ValidationError{
				Inv:     "INV-8",
				Message: fmt.Sprintf("CapabilityGraph.Edges[%d] From/To required", i),
			}
		}
		if _, ok := nodeIDs[edge.From]; !ok {
			return &ValidationError{
				Inv:     "INV-8",
				Message: fmt.Sprintf("CapabilityGraph.Edges[%d].From=%q not found in Nodes", i, edge.From),
			}
		}
		if _, ok := nodeIDs[edge.To]; !ok {
			return &ValidationError{
				Inv:     "INV-8",
				Message: fmt.Sprintf("CapabilityGraph.Edges[%d].To=%q not found in Nodes", i, edge.To),
			}
		}
		// 边自身的 ProtocolLoss 也禁止 silent drop（与 node / projection / graph 三层一致）。
		for j, loss := range edge.ProtocolLoss {
			if loss.IsSilentDrop() {
				return &ValidationError{
					Inv:     "INV-7",
					Message: fmt.Sprintf("CapabilityGraph.Edges[%d].ProtocolLoss[%d] is a silent drop", i, j),
				}
			}
		}
		if edge.Type == EdgeMutuallyExclusive {
			a, b := edge.From, edge.To
			if a > b {
				a, b = b, a
			}
			key := edgePair{from: a, to: b}
			if _, dup := mutexPairs[key]; dup {
				return &ValidationError{
					Inv:     "INV-9",
					Message: fmt.Sprintf("CapabilityGraph.Edges[%d] EdgeMutuallyExclusive %q↔%q already declared (no bidirectional)", i, edge.From, edge.To),
				}
			}
			mutexPairs[key] = struct{}{}
		}
	}

	for j, loss := range g.ProtocolLoss {
		if loss.IsSilentDrop() {
			return &ValidationError{
				Inv:     "INV-7",
				Message: fmt.Sprintf("CapabilityGraph.ProtocolLoss[%d] is a silent drop", j),
			}
		}
	}
	return nil
}

// validateNodeTaggedUnion 校验 Kind 必须与恰好一个 nullable payload pointer 对应。
func validateNodeTaggedUnion(node CapabilityNode, idx int) error {
	expected, supported := nodePayloadByKind(node)
	if !supported {
		return &ValidationError{
			Inv:     "INV-3",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].Kind=%q is not in CapabilityKind enum", idx, node.Kind),
		}
	}
	nonNil := nonNilPayloads(node)
	if len(nonNil) != 1 {
		return &ValidationError{
			Inv:     "INV-3",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d] tagged-union violation: expected exactly one payload non-nil for Kind=%q, got %d non-nil [%s]", idx, node.Kind, len(nonNil), strings.Join(nonNil, ",")),
		}
	}
	if nonNil[0] != expected {
		return &ValidationError{
			Inv:     "INV-3",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d] tagged-union mismatch: Kind=%q expects %q payload, got %q", idx, node.Kind, expected, nonNil[0]),
		}
	}
	return nil
}

// capabilityPayloadFieldName 是 CapabilityKind → Go struct payload 字段名的真相源。
//
// 与 capability_graph.go 中的 AllCapabilityKinds + CapabilityNode 14 个 nullable pointer
// 字段保持单一来源；init() 校验长度一致避免 enum 漂移。validator 与 projection 校验都
// 经此 map 查询，避免 switch + slice 双真相源（Lane A renew L5）。
var capabilityPayloadFieldName = map[CapabilityKind]string{
	CapabilityText:             "Text",
	CapabilityToolUse:          "ToolUse",
	CapabilityToolResult:       "ToolResult",
	CapabilityThinking:         "Thinking",
	CapabilityCacheControl:     "CacheControl",
	CapabilityStructuredOutput: "StructuredOutput",
	CapabilityComputerUse:      "ComputerUse",
	CapabilityFile:             "File",
	CapabilityImage:            "Image",
	CapabilityAudio:            "Audio",
	CapabilityVideo:            "Video",
	CapabilityLiveSession:      "LiveSession",
	CapabilityBatch:            "Batch",
	CapabilityMCPServer:        "MCPServer",
	CapabilityDataRetention:    "DataRetention",
}

// edgeTypeSet 是 AllEdgeTypes 的 O(1) 查询副本；validator 用这个判断合法 edge.Type。
var edgeTypeSet = func() map[CapabilityEdgeType]struct{} {
	m := make(map[CapabilityEdgeType]struct{}, len(AllEdgeTypes))
	for _, t := range AllEdgeTypes {
		m[t] = struct{}{}
	}
	return m
}()

func init() {
	// AllCapabilityKinds 与 capabilityPayloadFieldName 必须同长；任一加新成员后另一处必须同步。
	if len(AllCapabilityKinds) != len(capabilityPayloadFieldName) {
		panic(fmt.Sprintf(
			"proto enum drift: AllCapabilityKinds=%d, capabilityPayloadFieldName=%d",
			len(AllCapabilityKinds), len(capabilityPayloadFieldName),
		))
	}
	for _, k := range AllCapabilityKinds {
		if _, ok := capabilityPayloadFieldName[k]; !ok {
			panic(fmt.Sprintf("proto enum drift: AllCapabilityKinds member %q missing in capabilityPayloadFieldName", k))
		}
	}
}

// nodePayloadByKind 返回 Kind 对应的 payload 字段名 + 是否合法 Kind。
func nodePayloadByKind(node CapabilityNode) (string, bool) {
	name, ok := capabilityPayloadFieldName[node.Kind]
	return name, ok
}

// nonNilPayloads 列出 node 中非 nil 的 payload 字段名。
func nonNilPayloads(node CapabilityNode) []string {
	var names []string
	if node.Text != nil {
		names = append(names, "Text")
	}
	if node.ToolUse != nil {
		names = append(names, "ToolUse")
	}
	if node.ToolResult != nil {
		names = append(names, "ToolResult")
	}
	if node.Thinking != nil {
		names = append(names, "Thinking")
	}
	if node.CacheControl != nil {
		names = append(names, "CacheControl")
	}
	if node.StructuredOutput != nil {
		names = append(names, "StructuredOutput")
	}
	if node.ComputerUse != nil {
		names = append(names, "ComputerUse")
	}
	if node.File != nil {
		names = append(names, "File")
	}
	if node.Image != nil {
		names = append(names, "Image")
	}
	if node.Audio != nil {
		names = append(names, "Audio")
	}
	if node.Video != nil {
		names = append(names, "Video")
	}
	if node.LiveSession != nil {
		names = append(names, "LiveSession")
	}
	if node.Batch != nil {
		names = append(names, "Batch")
	}
	if node.MCPServer != nil {
		names = append(names, "MCPServer")
	}
	if node.DataRetention != nil {
		names = append(names, "DataRetention")
	}
	return names
}

// validateProviderProjection 校验 projection 层规则：
//
//   - Capability 必填且必须在 AllCapabilityKinds 中（capability enum）
//   - Verdict 必填且必须在 AllProjectionVerdicts 中
//   - Verdict != preserved 时 ProtocolLoss 至少一条且不能 silent drop
//   - Verdict == native_required 时 NativePath 必填
func validateProviderProjection(p *ProviderProjection) error {
	for i, cp := range p.CapabilityResults {
		if cp.Capability == "" {
			return &ValidationError{
				Inv:     "INV-3",
				Message: fmt.Sprintf("ProviderProjection.CapabilityResults[%d].Capability is required", i),
			}
		}
		if _, ok := capabilityPayloadFieldName[cp.Capability]; !ok {
			return &ValidationError{
				Inv:     "INV-3",
				Message: fmt.Sprintf("ProviderProjection.CapabilityResults[%d].Capability=%q is not in CapabilityKind enum", i, cp.Capability),
			}
		}
		if cp.Verdict == "" {
			return &ValidationError{
				Inv:     "INV-7",
				Message: fmt.Sprintf("ProviderProjection.CapabilityResults[%d].Verdict is required", i),
			}
		}
		if !isProjectionVerdict(cp.Verdict) {
			return &ValidationError{
				Inv:     "INV-7",
				Message: fmt.Sprintf("ProviderProjection.CapabilityResults[%d].Verdict=%q is not in ProjectionVerdict enum", i, cp.Verdict),
			}
		}
		if cp.Verdict != ProjectionPreserved && len(cp.ProtocolLoss) == 0 {
			return &ValidationError{
				Inv:     "INV-7",
				Message: fmt.Sprintf("ProviderProjection.CapabilityResults[%d] verdict=%q must have at least one ProtocolLoss entry", i, cp.Verdict),
			}
		}
		for j, loss := range cp.ProtocolLoss {
			if loss.IsSilentDrop() {
				return &ValidationError{
					Inv:     "INV-7",
					Message: fmt.Sprintf("ProviderProjection.CapabilityResults[%d].ProtocolLoss[%d] is a silent drop", i, j),
				}
			}
		}
		if cp.Verdict == ProjectionNativeRequired && cp.NativePath == "" {
			return &ValidationError{
				Inv:     "INV-7",
				Message: fmt.Sprintf("ProviderProjection.CapabilityResults[%d] verdict=native_required must specify NativePath", i),
			}
		}
	}
	return nil
}

// isProjectionVerdict 是 ProjectionVerdict enum 的快速判定。
func isProjectionVerdict(v ProjectionVerdict) bool {
	switch v {
	case ProjectionPreserved, ProjectionLossy, ProjectionUnsupported, ProjectionNativeRequired:
		return true
	}
	return false
}

// validateStreamPlan 校验 StreamPlan mode 与 fallback 默认值：
//
//   - StreamPlan.Mode 必填且必须在 StreamMode 枚举内（buffered/streaming/replay）
//   - 默认 MidStreamFallbackNone；非 none 拒绝（D9 留位，P-8 才能改）
func validateStreamPlan(s *StreamPlan) error {
	if s.Mode == "" {
		return &ValidationError{Inv: "INV-13", Message: "StreamPlan.Mode is required"}
	}
	switch s.Mode {
	case StreamModeBuffered, StreamModeStreaming, StreamModeReplay:
	default:
		return &ValidationError{
			Inv:     "INV-13",
			Message: fmt.Sprintf("StreamPlan.Mode=%q is not in StreamMode enum", s.Mode),
		}
	}
	if s.MidStreamFallbackPolicy != MidStreamFallbackNone {
		return &ValidationError{
			Inv:     "INV-11",
			Message: fmt.Sprintf("StreamPlan.MidStreamFallbackPolicy must be %q at P-0; got %q (P-8 才允许其它值)", MidStreamFallbackNone, s.MidStreamFallbackPolicy),
		}
	}
	return nil
}

// validatePolicy 校验 DataRetentionLabel 严格枚举。
func validatePolicy(p *Policy) error {
	v := p.DataRetention.Value
	if v == "" {
		return &ValidationError{Inv: "INV-10", Message: "Policy.DataRetention.Value is required"}
	}
	for _, allowed := range AllDataRetentionLabels {
		if v == allowed {
			return nil
		}
	}
	return &ValidationError{
		Inv:     "INV-10",
		Message: fmt.Sprintf("Policy.DataRetention.Value=%q not in 5-vocabulary enum", v),
	}
}

// validateExtensions 校验 key 前缀必须 vendor: 或 experimental:。
func validateExtensions(ext map[string]json.RawMessage) error {
	if ext == nil {
		return nil
	}
	for k := range ext {
		if !strings.HasPrefix(k, "vendor:") && !strings.HasPrefix(k, "experimental:") {
			return &ValidationError{
				Inv:     "INV-12",
				Message: fmt.Sprintf("Extensions key %q must have prefix vendor: or experimental:", k),
			}
		}
	}
	return nil
}

// validateCapabilityNodeMeta 校验 CapabilityNode.StreamReady 必须在 StreamReadiness enum 内。
//
// StreamReady 是 node 顶层字段（与 payload 独立），所有 14 capability 通用。
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

// IsValidationError 提供 errors.Is 风格的 sentinel 检测便利。
func IsValidationError(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}
