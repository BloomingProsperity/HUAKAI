package proto

import "fmt"

// edgePair 是 capability_graph 边的有向键（from → to）。
// 用 typed struct 替代 "from|to" 字符串拼接以避免节点 ID 含 "|" 时的碰撞绕过。
type edgePair struct{ from, to string }

// validateCrossRefs 校验需要在节点 / projection / source_ref 之间解析引用的 INV：
//
//   - CacheControlNode.BreakpointRefs：每个非空 ref 必须解析到现有 node；
//     Scope ∈ {block, message} 时 BreakpointRefs 不得为空
//   - ComputerUseNode.ScreenshotRef 非空时必须解析到 image/file node
//   - （D3 收口部分）LiveSessionNode.Modalities ⊂ {text, audio, video}
//   - （D3 收口部分）MCPServerNode.ServerLabel 必填
//   - CapabilityProjection.NodeID 非空时必须解析到现有 node，且 Capability == node.Kind
//   - NodeSourceRef 索引：MessageIndex/BlockIndex/EventIndex 非负；MessageIndex/EventIndex
//     若在范围内可知时不得越界
//
// 不在 D3 范围（推迟到 D5 — 需要先更新 fixture）：
//
//   - ToolResultNode.ToolCallID 必须匹配同图 ToolUseNode（tool_result_minimal 需补 tool_use）
//   - BatchNode.InputRef 必须指向 FileNode（batch_minimal 需补 file node）
//   - 全 LiveSession.ToolNodeIDs/MCPServer.InvocationNodeIDs/ResultNodeIDs 引用解析
//
// 调用方：ValidateEnvelope 在 validateProviderProjection 之后调用本入口；hot path 不走此处。
func validateCrossRefs(env *HCSFEnvelope) error {
	nodeIDIndex := make(map[string]CapabilityKind, len(env.CapabilityGraph.Nodes))
	for _, n := range env.CapabilityGraph.Nodes {
		nodeIDIndex[n.ID] = n.Kind
	}

	// toolCallIDToNodeID 把 ToolUse.ToolCallID 映射到节点 ID，用于解析 ToolResult.ToolCallID。
	//
	// 同 envelope 内多个 ToolUse 共享同一 ToolCallID 视为歧义（issue-derived：sub2api#1552
	// tool_args_lost 模式 — 重复 ID 导致 ToolResult 无法确定指向哪个 ToolUse），直接拒绝。
	toolCallIDToNodeID := make(map[string]string)
	for i, n := range env.CapabilityGraph.Nodes {
		if n.Kind == CapabilityToolUse && n.ToolUse != nil && n.ToolUse.ToolCallID != "" {
			if existing, dup := toolCallIDToNodeID[n.ToolUse.ToolCallID]; dup {
				return &ValidationError{
					Inv:     "INV-19",
					Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].tool_use.tool_call_id=%q is also used by node %q (duplicate ToolCallID is ambiguous)", i, n.ToolUse.ToolCallID, existing),
				}
			}
			toolCallIDToNodeID[n.ToolUse.ToolCallID] = n.ID
		}
	}

	// requiresEdgeSet 用 typed edgePair（包级声明）标记是否存在 type=requires 边。
	// 不用 "from|to" 字符串拼接 — 节点 ID 含 "|" 时会碰撞绕过校验。
	requiresEdgeSet := make(map[edgePair]struct{})
	for _, e := range env.CapabilityGraph.Edges {
		if e.Type == EdgeRequires {
			requiresEdgeSet[edgePair{from: e.From, to: e.To}] = struct{}{}
		}
	}

	msgCount := len(env.Messages)
	streamCount := len(env.StreamEvents)

	for i, node := range env.CapabilityGraph.Nodes {
		if err := validateNodeSourceRef(node, i, msgCount, streamCount); err != nil {
			return err
		}
		switch node.Kind {
		case CapabilityCacheControl:
			if err := validateCacheBreakpointRefs(node.CacheControl, i, nodeIDIndex); err != nil {
				return err
			}
		case CapabilityComputerUse:
			if err := validateComputerScreenshotRef(node.ComputerUse, i, nodeIDIndex); err != nil {
				return err
			}
		case CapabilityLiveSession:
			if err := validateLiveSessionModalities(node.LiveSession, i); err != nil {
				return err
			}
			if err := validateLiveSessionToolNodeRefs(node.LiveSession, i, nodeIDIndex); err != nil {
				return err
			}
		case CapabilityMCPServer:
			if err := validateMCPServerLabel(node.MCPServer, i); err != nil {
				return err
			}
			if err := validateMCPServerNodeRefs(node.MCPServer, i, nodeIDIndex); err != nil {
				return err
			}
		case CapabilityToolResult:
			if err := validateToolResultToolCallRef(node.ToolResult, node.ID, i, toolCallIDToNodeID, requiresEdgeSet); err != nil {
				return err
			}
		case CapabilityBatch:
			if err := validateBatchFileRefs(node.Batch, i, nodeIDIndex); err != nil {
				return err
			}
		}
	}
	if err := validateProjectionNodeRefs(&env.ProviderProjection, nodeIDIndex); err != nil {
		return err
	}
	if err := validateDataRetentionConsistency(env); err != nil {
		return err
	}
	return validateProtocolLossEntriesAll(env, nodeIDIndex)
}

// validateNodeSourceRef 校验 NodeSourceRef 索引非负 + 范围内。
//
// MessageIndex/EventIndex 仅在对应集合非空时检查上界；BlockIndex 只查非负
// （上界需要拉取 Messages[MessageIndex].Content 嵌套深度，留 P-2 ClientAdapter 处理）。
func validateNodeSourceRef(node CapabilityNode, idx, msgCount, streamCount int) error {
	src := node.Source
	if src == nil {
		return nil
	}
	if src.MessageIndex != nil {
		if *src.MessageIndex < 0 {
			return &ValidationError{
				Inv:     "INV-46",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].source.message_index=%d must be >= 0", idx, *src.MessageIndex),
			}
		}
		if msgCount > 0 && *src.MessageIndex >= msgCount {
			return &ValidationError{
				Inv:     "INV-46",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].source.message_index=%d out of range (messages len=%d)", idx, *src.MessageIndex, msgCount),
			}
		}
	}
	if src.BlockIndex != nil && *src.BlockIndex < 0 {
		return &ValidationError{
			Inv:     "INV-46",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].source.block_index=%d must be >= 0", idx, *src.BlockIndex),
		}
	}
	if src.EventIndex != nil {
		if *src.EventIndex < 0 {
			return &ValidationError{
				Inv:     "INV-46",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].source.event_index=%d must be >= 0", idx, *src.EventIndex),
			}
		}
		if streamCount > 0 && *src.EventIndex >= streamCount {
			return &ValidationError{
				Inv:     "INV-46",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].source.event_index=%d out of range (stream_events len=%d)", idx, *src.EventIndex, streamCount),
			}
		}
	}
	return nil
}

// validateCacheBreakpointRefs 校验 CacheControl.BreakpointRefs 解析 + Scope 条件。
func validateCacheBreakpointRefs(c *CacheControlNode, idx int, nodeIDIndex map[string]CapabilityKind) error {
	if c == nil {
		return nil
	}
	if c.Scope == CacheScopeBlock || c.Scope == CacheScopeMessage {
		if len(c.BreakpointRefs) == 0 {
			return &ValidationError{
				Inv:     "INV-26",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].cache_control.breakpoint_refs must be non-empty when scope=%q", idx, c.Scope),
			}
		}
	}
	for j, ref := range c.BreakpointRefs {
		if ref == "" {
			return &ValidationError{
				Inv:     "INV-26",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].cache_control.breakpoint_refs[%d] must not be empty string", idx, j),
			}
		}
		if _, ok := nodeIDIndex[ref]; !ok {
			return &ValidationError{
				Inv:     "INV-26",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].cache_control.breakpoint_refs[%d]=%q does not resolve to any node ID", idx, j, ref),
			}
		}
	}
	return nil
}

// validateComputerScreenshotRef 校验 ScreenshotRef 非空时必须指向 image/file node。
func validateComputerScreenshotRef(cu *ComputerUseNode, idx int, nodeIDIndex map[string]CapabilityKind) error {
	if cu == nil || cu.ScreenshotRef == "" {
		return nil
	}
	kind, ok := nodeIDIndex[cu.ScreenshotRef]
	if !ok {
		return &ValidationError{
			Inv:     "INV-35",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].computer_use.screenshot_ref=%q does not resolve to any node ID", idx, cu.ScreenshotRef),
		}
	}
	if kind != CapabilityImage && kind != CapabilityFile {
		return &ValidationError{
			Inv:     "INV-35",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].computer_use.screenshot_ref=%q points to kind=%q (must be image or file)", idx, cu.ScreenshotRef, kind),
		}
	}
	return nil
}

// validateLiveSessionModalities 校验 收口部分：Modalities ⊂ {text, audio, video}。
func validateLiveSessionModalities(ls *LiveSessionNode, idx int) error {
	if ls == nil {
		return nil
	}
	for j, m := range ls.Modalities {
		if _, ok := liveModalitySet[m]; !ok {
			return &ValidationError{
				Inv:     "INV-41",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].live_session.modalities[%d]=%q is not in modality enum {text,audio,video}", idx, j, m),
			}
		}
	}
	return nil
}

// validateMCPServerLabel 校验 收口部分：ServerLabel 必填。
//
// AllowedOperations 允许空数组（capability_mcp.go 注释）；InvocationNodeIDs/ResultNodeIDs
// 的引用解析推迟 D5（需 fixture 同时包含 mcp + tool_use/tool_result）。
func validateMCPServerLabel(mcp *MCPServerNode, idx int) error {
	if mcp == nil {
		return nil
	}
	if mcp.ServerLabel == "" {
		return &ValidationError{
			Inv:     "INV-42",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].mcp_server.server_label is required", idx),
		}
	}
	return nil
}

// validateToolResultToolCallRef 校验 ToolResult.ToolCallID 必须匹配同图 ToolUse +
// 存在 requires edge（tool_result node → tool_use node）。
//
// 两步守门避免 issue mode sub2api#1552 / portkey#1579 / litellm#27468 的 silent drop：
//
//  1. ToolCallID 字符串匹配 — 保证语义对应
//  2. requires edge 存在 — 保证 capability_graph 可寻址（projection / audit 路径都能拉到链）
func validateToolResultToolCallRef(payload *ToolResultNode, nodeID string, idx int, toolCallIDToNodeID map[string]string, requiresEdgeSet map[edgePair]struct{}) error {
	if payload == nil || payload.ToolCallID == "" {
		return nil
	}
	toolUseNodeID, ok := toolCallIDToNodeID[payload.ToolCallID]
	if !ok {
		return &ValidationError{
			Inv:     "INV-19",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].tool_result.tool_call_id=%q does not match any tool_use node in the envelope", idx, payload.ToolCallID),
		}
	}
	if _, hasEdge := requiresEdgeSet[edgePair{from: nodeID, to: toolUseNodeID}]; !hasEdge {
		return &ValidationError{
			Inv:     "INV-19",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].tool_result is missing required `requires` edge from %q → %q", idx, nodeID, toolUseNodeID),
		}
	}
	return nil
}

// validateBatchFileRefs 校验 BatchNode.InputRef/OutputRef/ErrorRef 非空时必须解析到 FileNode。
//
// 防止"input_ref 直接藏 provider file id 字符串"模式 — 外部 provider file id 应放入 FileNode.Locator，
// BatchNode 只承担图内引用语义。
func validateBatchFileRefs(payload *BatchNode, idx int, nodeIDIndex map[string]CapabilityKind) error {
	if payload == nil {
		return nil
	}
	checks := []struct {
		field string
		value string
	}{
		{"input_ref", payload.InputRef},
		{"output_ref", payload.OutputRef},
		{"error_ref", payload.ErrorRef},
	}
	for _, c := range checks {
		if c.value == "" {
			continue
		}
		kind, ok := nodeIDIndex[c.value]
		if !ok {
			return &ValidationError{
				Inv:     "INV-28",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].batch.%s=%q does not resolve to any node ID (provider file id 应放入 FileNode.Locator)", idx, c.field, c.value),
			}
		}
		if kind != CapabilityFile {
			return &ValidationError{
				Inv:     "INV-28",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].batch.%s=%q points to kind=%q (must be file)", idx, c.field, c.value, kind),
			}
		}
	}
	return nil
}

// validateLiveSessionToolNodeRefs 校验 收口部分：ToolNodeIDs 必须解析到 tool_use/computer_use/mcp_server。
func validateLiveSessionToolNodeRefs(payload *LiveSessionNode, idx int, nodeIDIndex map[string]CapabilityKind) error {
	if payload == nil {
		return nil
	}
	for j, ref := range payload.ToolNodeIDs {
		if ref == "" {
			return &ValidationError{
				Inv:     "INV-41",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].live_session.tool_node_ids[%d] must not be empty string", idx, j),
			}
		}
		kind, ok := nodeIDIndex[ref]
		if !ok {
			return &ValidationError{
				Inv:     "INV-41",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].live_session.tool_node_ids[%d]=%q does not resolve to any node ID", idx, j, ref),
			}
		}
		if kind != CapabilityToolUse && kind != CapabilityComputerUse && kind != CapabilityMCPServer {
			return &ValidationError{
				Inv:     "INV-41",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].live_session.tool_node_ids[%d]=%q points to kind=%q (must be tool_use/computer_use/mcp_server)", idx, j, ref, kind),
			}
		}
	}
	return nil
}

// validateMCPServerNodeRefs 校验 收口部分：MCPServer.InvocationNodeIDs/ResultNodeIDs 引用解析。
//
//   - InvocationNodeIDs[i] 必须指 tool_use / computer_use node
//   - ResultNodeIDs[i] 必须指 tool_result node
func validateMCPServerNodeRefs(payload *MCPServerNode, idx int, nodeIDIndex map[string]CapabilityKind) error {
	if payload == nil {
		return nil
	}
	for j, ref := range payload.InvocationNodeIDs {
		if ref == "" {
			return &ValidationError{
				Inv:     "INV-42",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].mcp_server.invocation_node_ids[%d] must not be empty string", idx, j),
			}
		}
		kind, ok := nodeIDIndex[ref]
		if !ok {
			return &ValidationError{
				Inv:     "INV-42",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].mcp_server.invocation_node_ids[%d]=%q does not resolve to any node ID", idx, j, ref),
			}
		}
		if kind != CapabilityToolUse && kind != CapabilityComputerUse {
			return &ValidationError{
				Inv:     "INV-42",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].mcp_server.invocation_node_ids[%d]=%q points to kind=%q (must be tool_use or computer_use)", idx, j, ref, kind),
			}
		}
	}
	for j, ref := range payload.ResultNodeIDs {
		if ref == "" {
			return &ValidationError{
				Inv:     "INV-42",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].mcp_server.result_node_ids[%d] must not be empty string", idx, j),
			}
		}
		kind, ok := nodeIDIndex[ref]
		if !ok {
			return &ValidationError{
				Inv:     "INV-42",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].mcp_server.result_node_ids[%d]=%q does not resolve to any node ID", idx, j, ref),
			}
		}
		if kind != CapabilityToolResult {
			return &ValidationError{
				Inv:     "INV-42",
				Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].mcp_server.result_node_ids[%d]=%q points to kind=%q (must be tool_result)", idx, j, ref, kind),
			}
		}
	}
	return nil
}

// validateProjectionNodeRefs 校验 projection.NodeID 非空时必须解析 + Capability == node.Kind。
func validateProjectionNodeRefs(p *ProviderProjection, nodeIDIndex map[string]CapabilityKind) error {
	for i, cp := range p.CapabilityResults {
		if cp.NodeID == "" {
			continue
		}
		kind, ok := nodeIDIndex[cp.NodeID]
		if !ok {
			return &ValidationError{
				Inv:     "INV-43",
				Message: fmt.Sprintf("ProviderProjection.CapabilityResults[%d].NodeID=%q does not resolve to any node ID", i, cp.NodeID),
			}
		}
		if kind != cp.Capability {
			return &ValidationError{
				Inv:     "INV-43",
				Message: fmt.Sprintf("ProviderProjection.CapabilityResults[%d] Capability=%q does not match node Kind=%q at NodeID=%q", i, cp.Capability, kind, cp.NodeID),
			}
		}
	}
	return nil
}

// validateDataRetentionConsistency 校验 graph 中 data_retention node 与 Policy.DataRetention 一致。
//
// P-1 仅支持 0 或 1 个 data_retention node；多 node 合并语义推迟 P-2。
// 一致性范围：Value 必须相等；Enforcement 必须相等；Region/RequestStore/EvidenceRef 若双侧都设则必须相等。
func validateDataRetentionConsistency(env *HCSFEnvelope) error {
	var nodes []*DataRetentionNode
	var nodeIdx []int
	for i, n := range env.CapabilityGraph.Nodes {
		if n.Kind == CapabilityDataRetention && n.DataRetention != nil {
			nodes = append(nodes, n.DataRetention)
			nodeIdx = append(nodeIdx, i)
		}
	}
	if len(nodes) > 1 {
		return &ValidationError{
			Inv:     "INV-33",
			Message: fmt.Sprintf("CapabilityGraph contains %d data_retention nodes; P-1 only supports 0 or 1 (multi-node merge semantics deferred to P-2)", len(nodes)),
		}
	}
	if len(nodes) == 0 {
		return nil
	}
	node := nodes[0]
	policy := env.Policy.DataRetention
	if node.Value != policy.Value {
		return &ValidationError{
			Inv:     "INV-33",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].data_retention.value=%q does not match Policy.DataRetention.value=%q", nodeIdx[0], node.Value, policy.Value),
		}
	}
	if node.Enforcement != policy.Enforcement {
		return &ValidationError{
			Inv:     "INV-33",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].data_retention.enforcement=%q does not match Policy.DataRetention.enforcement=%q", nodeIdx[0], node.Enforcement, policy.Enforcement),
		}
	}
	if node.Region != "" && policy.Region != "" && node.Region != policy.Region {
		return &ValidationError{
			Inv:     "INV-33",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].data_retention.region=%q does not match Policy.DataRetention.region=%q", nodeIdx[0], node.Region, policy.Region),
		}
	}
	if node.RequestStore != nil && policy.RequestStore != nil && *node.RequestStore != *policy.RequestStore {
		return &ValidationError{
			Inv:     "INV-33",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].data_retention.request_store=%v does not match Policy.DataRetention.request_store=%v", nodeIdx[0], *node.RequestStore, *policy.RequestStore),
		}
	}
	if node.EvidenceRef != "" && policy.EvidenceRef != "" && node.EvidenceRef != policy.EvidenceRef {
		return &ValidationError{
			Inv:     "INV-33",
			Message: fmt.Sprintf("CapabilityGraph.Nodes[%d].data_retention.evidence_ref=%q does not match Policy.DataRetention.evidence_ref=%q", nodeIdx[0], node.EvidenceRef, policy.EvidenceRef),
		}
	}
	return nil
}

// validateProtocolLossEntriesAll 扫所有四处 ProtocolLossEntry 做字段守门。
//
// 四处位置：node-level / edge-level / graph-level / projection-level。
// 对每条 entry：Severity 若非空 ∈ {info,warning,error}；NodeID 若非空 resolve；Capability 若非空 ∈ 14 vocab。
//
// 与 silent drop 守门关系：守 silent drop（任意可读字段存在即合法），守 enum + 引用形态。两者互补。
func validateProtocolLossEntriesAll(env *HCSFEnvelope, nodeIDIndex map[string]CapabilityKind) error {
	for i, n := range env.CapabilityGraph.Nodes {
		for j, e := range n.ProtocolLoss {
			if err := checkProtocolLossEntry(e, fmt.Sprintf("CapabilityGraph.Nodes[%d].ProtocolLoss[%d]", i, j), nodeIDIndex); err != nil {
				return err
			}
		}
	}
	for i, e := range env.CapabilityGraph.Edges {
		for j, l := range e.ProtocolLoss {
			if err := checkProtocolLossEntry(l, fmt.Sprintf("CapabilityGraph.Edges[%d].ProtocolLoss[%d]", i, j), nodeIDIndex); err != nil {
				return err
			}
		}
	}
	for j, l := range env.CapabilityGraph.ProtocolLoss {
		if err := checkProtocolLossEntry(l, fmt.Sprintf("CapabilityGraph.ProtocolLoss[%d]", j), nodeIDIndex); err != nil {
			return err
		}
	}
	for i, cp := range env.ProviderProjection.CapabilityResults {
		for j, l := range cp.ProtocolLoss {
			if err := checkProtocolLossEntry(l, fmt.Sprintf("ProviderProjection.CapabilityResults[%d].ProtocolLoss[%d]", i, j), nodeIDIndex); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkProtocolLossEntry 是单条守门器：Severity/NodeID/Capability 三字段独立检查。
func checkProtocolLossEntry(e ProtocolLossEntry, where string, nodeIDIndex map[string]CapabilityKind) error {
	if e.Severity != "" {
		if _, ok := protocolLossSeveritySet[e.Severity]; !ok {
			return &ValidationError{
				Inv:     "INV-45",
				Message: fmt.Sprintf("%s severity=%q is not in {info,warning,error}", where, e.Severity),
			}
		}
	}
	if e.NodeID != "" {
		if _, ok := nodeIDIndex[e.NodeID]; !ok {
			return &ValidationError{
				Inv:     "INV-45",
				Message: fmt.Sprintf("%s node_id=%q does not resolve to any node ID", where, e.NodeID),
			}
		}
	}
	if e.Capability != "" {
		if _, ok := capabilityPayloadFieldName[e.Capability]; !ok {
			return &ValidationError{
				Inv:     "INV-45",
				Message: fmt.Sprintf("%s capability=%q is not in CapabilityKind enum", where, e.Capability),
			}
		}
	}
	return nil
}
