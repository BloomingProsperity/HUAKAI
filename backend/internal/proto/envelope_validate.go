package proto

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ValidationError 描述一次 envelope 校验失败；包含违反的 INV 编号便于定位。
type ValidationError struct {
	Inv     string // 如 "INV-3"
	Message string // 人读说明
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("envelope %s: %s", e.Inv, e.Message)
}

// ValidateEnvelope 对 HCSFEnvelope 做 INV-1..13 中所有结构性 / 语义性校验。
//
//   - INV-1 不在此校验（属于 marshal/unmarshal round-trip 不变量，由测试覆盖）
//   - INV-2 同上（nil/empty slice 等价由 encoding/json + omitempty 自然满足）
//   - INV-3 tagged-union 一致性
//   - INV-4 Version=="0.4"
//   - INV-5 RequestMeta 必填字段非空
//   - INV-6 BufferedResponse + StreamEvents 至多一个非 nil
//   - INV-7 ProtocolLoss 不可作为 silent drop（含 node / edge / projection 三层）
//   - INV-8 Edge 必填 ID/Type/From/To；Type 必须在 AllEdgeTypes 中；From/To 必须命中 Nodes
//   - INV-9 EdgeMutuallyExclusive 不可双向
//   - INV-10 DataRetentionLabel 严格枚举
//   - INV-11 MidStreamFallbackPolicy 默认 none
//   - INV-12 Extensions key 前缀
//   - INV-13 StreamPlan.Mode 必填且必须在 StreamMode 枚举内
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
	return nil
}

// validateVersion 校验 INV-4。
func validateVersion(env *HCSFEnvelope) error {
	if env.Version != HCSFVersion {
		return &ValidationError{
			Inv:     "INV-4",
			Message: fmt.Sprintf("Version must be %q, got %q", HCSFVersion, env.Version),
		}
	}
	return nil
}

// validateRequestMeta 校验 INV-5。
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

// validateEnvelopeShape 校验 INV-6（BufferedResponse + StreamEvents 至多一个非 nil）。
//
// 形态推导规则（envelope.go:10-17）：StreamEvents non-nil 即视为 replay shape，包括
// `[]CanonicalEvent{}` 显式空切片。BufferedResponse + StreamEvents:nil 合法；
// BufferedResponse + StreamEvents:[] 合法（用户显式声明 buffered 形态、StreamEvents 不参与）；
// BufferedResponse + StreamEvents:[event{...}] 违反 INV-6。
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

// validateCapabilityGraph 校验 INV-3 / INV-7 / INV-8 / INV-9。
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
		for j, loss := range node.ProtocolLoss {
			if loss.IsSilentDrop() {
				return &ValidationError{
					Inv:     "INV-7",
					Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].ProtocolLoss[%d] is a silent drop (no Reason/Note/Verdict/Code)", i, j),
				}
			}
		}
	}

	mutexPairs := make(map[string]struct{}, len(g.Edges))
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
			key := a + "|" + b
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

// validateNodeTaggedUnion 校验 INV-3：Kind 必须与恰好一个 nullable payload pointer 对应。
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

// validateProviderProjection 校验 INV-7 / INV-3 在 projection 层的延伸：
//
//   - Capability 必填且必须在 AllCapabilityKinds 中（INV-3 capability enum）
//   - Verdict 必填且必须在 AllProjectionVerdicts 中
//   - Verdict != preserved 时 ProtocolLoss 至少一条且不能 silent drop（INV-7）
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

// validateStreamPlan 校验 INV-11 与 INV-13：
//
//   - INV-13 StreamPlan.Mode 必填且必须在 StreamMode 枚举内（buffered/streaming/replay）
//   - INV-11 P-0 默认 MidStreamFallbackNone；非 none 拒绝（D9 留位，P-8 才能改）
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

// validatePolicy 校验 INV-10（DataRetentionLabel 严格枚举）。
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

// validateExtensions 校验 INV-12（key 前缀必须 vendor: 或 experimental:）。
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

// IsValidationError 提供 errors.Is 风格的 sentinel 检测便利。
func IsValidationError(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}
