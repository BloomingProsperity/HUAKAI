package proto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// P-2 D1 anthropic_messages ClientAdapter — RequestToCanonical（D1 + D1.x）。
//
// 范围（P-2 synthesis §5.1 + Q5 决策 A：context 注入 RequestMetaSeed）：
//   - 入：Anthropic Messages API v1 request body（JSON）
//   - 出：HCSF v0.4 request envelope；含 RequestMeta + RequestControls +
//     Messages + CapabilityGraph（text / tool_use / tool_result + requires 边 /
//     image / cache_control / thinking）+ ProviderProjection client baseline。

// AnthropicMessagesClient 实现 ClientAdapter；零值即可使用。
// 注册到 ClientAdapterRegistry：reg.Register(ClientProtocolAnthropicMessages, &AnthropicMessagesClient{}).
type AnthropicMessagesClient struct{}

var _ ClientAdapter = (*AnthropicMessagesClient)(nil)

// RequestToCanonical 把 Anthropic Messages body 解为 HCSF v0.4 request envelope。
// 必要前置：调用方在 context 中通过 ContextWithRequestMetaSeed 注入 seed。
func (a *AnthropicMessagesClient) RequestToCanonical(ctx context.Context, raw []byte) (*HCSF, []ProtocolLossEntry, error) {
	seed, ok := RequestMetaSeedFromContext(ctx)
	if !ok {
		return nil, nil, ErrMissingRequestMetaSeed
	}

	var req anthropicMessagesRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, nil, fmt.Errorf("proto: anthropic_messages parse body: %w", err)
	}
	if req.Model == "" {
		return nil, nil, errors.New("proto: anthropic_messages missing required field 'model'")
	}
	if len(req.Messages) == 0 {
		return nil, nil, errors.New("proto: anthropic_messages 'messages' must not be empty")
	}

	env := NewEmptyEnvelope()
	if err := seed.ApplyToRequestMeta(&env.RequestMeta); err != nil {
		return nil, nil, err
	}
	env.RequestMeta.Model = req.Model

	// RequestControls
	env.RequestControls.MaxTokens = req.MaxTokens
	if env.RequestControls.MaxTokens == nil && req.MaxTokensToSample != nil {
		env.RequestControls.MaxTokens = req.MaxTokensToSample
	}
	env.RequestControls.Temperature = req.Temperature
	env.RequestControls.TopP = req.TopP
	if len(req.StopSequences) > 0 {
		env.RequestControls.StopSequences = req.StopSequences
	}
	if len(req.ToolChoice) > 0 {
		env.RequestControls.ToolChoice = req.ToolChoice
	}
	if len(req.Tools) > 0 {
		tools, err := parseAnthropicTools(req.Tools)
		if err != nil {
			return nil, nil, err
		}
		env.RequestControls.Tools = tools
	}

	var losses []ProtocolLossEntry
	attachRequestPassthroughFields(env, raw,
		"top_k",
		"context_management",
		"output_config",
		"output_format",
		"container",
	)

	// 解 system 字段（string 或 block array）
	if len(req.System) > 0 {
		sysStr, sysLoss, err := parseAnthropicSystemField(req.System)
		if err != nil {
			return nil, nil, err
		}
		env.RequestControls.SystemPrompt = sysStr
		losses = append(losses, sysLoss...)
	}

	// StreamPlan
	if req.Stream != nil && *req.Stream {
		env.StreamPlan.Mode = StreamModeStreaming
	}

	// Messages + CapabilityGraph
	// toolCallIDToNodeID 记录 tool_use 节点 → tool_use canonical id 映射，便于
	// 后续 tool_result 块 emit requires 边。
	toolCallIDToNodeID := make(map[string]string)
	nodeSeq := 0
	edgeSeq := 0
	for mi, m := range req.Messages {
		if m.Role == "" {
			return nil, nil, fmt.Errorf("proto: anthropic_messages messages[%d] missing role", mi)
		}
		cm := CanonicalMessage{Role: m.Role}

		blocks, blockLoss, err := parseAnthropicContent(m.Content, mi)
		if err != nil {
			return nil, nil, err
		}
		losses = append(losses, blockLoss...)

		for bi, b := range blocks {
			msgIdx := mi
			blkIdx := bi
			switch b.Type {
			case "text":
				cm.Content = append(cm.Content, CanonicalContentBlock{Type: "text", Text: b.Text})
				nodeSeq++
				nodeID := fmt.Sprintf("n_text_%d", nodeSeq)
				env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
					ID:          nodeID,
					Kind:        CapabilityText,
					StreamReady: StreamReadyYes,
					Source:      &NodeSourceRef{MessageIndex: &msgIdx, BlockIndex: &blkIdx},
					Text:        &TextNode{Role: m.Role, Block: CanonicalContentBlock{Type: "text", Text: b.Text}},
				})
				env.ProviderProjection.CapabilityResults = append(env.ProviderProjection.CapabilityResults, CapabilityProjection{
					Capability: CapabilityText, NodeID: nodeID, Verdict: ProjectionPreserved,
				})
				maybeEmitAnthropicCacheControl(env, b.CacheControl, nodeID, mi, bi, &nodeSeq, &edgeSeq)
			case "tool_use":
				if b.ID == "" || b.Name == "" {
					return nil, nil, fmt.Errorf("proto: anthropic_messages messages[%d].content[%d] tool_use missing id or name", mi, bi)
				}
				input := b.Input
				if len(input) == 0 {
					input = json.RawMessage("{}")
				}
				nodeSeq++
				nodeID := fmt.Sprintf("n_tool_use_%d", nodeSeq)
				cm.Content = append(cm.Content, CanonicalContentBlock{
					Type: "tool_use", CallID: b.ID, Name: b.Name, Input: input,
				})
				env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
					ID:          nodeID,
					Kind:        CapabilityToolUse,
					StreamReady: StreamReadyPartial,
					Source:      &NodeSourceRef{MessageIndex: &msgIdx, BlockIndex: &blkIdx},
					ToolUse: &ToolUseNode{
						ToolCallID: b.ID,
						Name:       b.Name,
						Input:      input,
						Status:     ToolNodeComplete,
					},
				})
				env.ProviderProjection.CapabilityResults = append(env.ProviderProjection.CapabilityResults, CapabilityProjection{
					Capability: CapabilityToolUse, NodeID: nodeID, Verdict: ProjectionPreserved,
				})
				toolCallIDToNodeID[b.ID] = nodeID
			case "web_search_tool", "server_tool_use":
				input := b.Raw
				if len(input) == 0 {
					input, _ = json.Marshal(b)
				}
				callID := b.ID
				if callID == "" {
					callID = fmt.Sprintf("anthropic_passthrough_%d_%d", mi, bi)
				}
				name := b.Name
				if name == "" {
					name = b.Type
				}
				nodeSeq++
				nodeID := fmt.Sprintf("n_tool_use_%d", nodeSeq)
				cm.Content = append(cm.Content, CanonicalContentBlock{
					Type: "tool_use", CallID: callID, Name: name, Input: input, Raw: input,
				})
				env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
					ID:          nodeID,
					Kind:        CapabilityToolUse,
					StreamReady: StreamReadyPartial,
					Source:      &NodeSourceRef{MessageIndex: &msgIdx, BlockIndex: &blkIdx},
					ToolUse: &ToolUseNode{
						ToolCallID:         callID,
						OriginalToolCallID: b.ID,
						Name:               name,
						Input:              input,
						Status:             ToolNodeComplete,
					},
				})
				env.ProviderProjection.CapabilityResults = append(env.ProviderProjection.CapabilityResults, CapabilityProjection{
					Capability: CapabilityToolUse, NodeID: nodeID, Verdict: ProjectionPreserved,
				})
				toolCallIDToNodeID[callID] = nodeID
			case "tool_result":
				if b.ToolUseID == "" {
					return nil, nil, fmt.Errorf("proto: anthropic_messages messages[%d].content[%d] tool_result missing tool_use_id", mi, bi)
				}
				resultBlocks, contentLoss, err := parseAnthropicToolResultContent(b.Content)
				if err != nil {
					return nil, nil, fmt.Errorf("proto: anthropic_messages messages[%d].content[%d] tool_result: %w", mi, bi, err)
				}
				losses = append(losses, contentLoss...)
				nodeSeq++
				nodeID := fmt.Sprintf("n_tool_result_%d", nodeSeq)
				rawResult, _ := json.Marshal(resultBlocks)
				cm.Content = append(cm.Content, CanonicalContentBlock{
					Type:       "tool_result",
					CallID:     b.ToolUseID,
					ToolResult: rawResult,
				})
				env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
					ID:          nodeID,
					Kind:        CapabilityToolResult,
					StreamReady: StreamReadyYes,
					Source:      &NodeSourceRef{MessageIndex: &msgIdx, BlockIndex: &blkIdx},
					ToolResult: &ToolResultNode{
						ToolCallID: b.ToolUseID,
						Content:    resultBlocks,
						Status:     ToolNodeComplete,
						IsError:    false,
					},
				})
				env.ProviderProjection.CapabilityResults = append(env.ProviderProjection.CapabilityResults, CapabilityProjection{
					Capability: CapabilityToolResult, NodeID: nodeID, Verdict: ProjectionPreserved,
				})
				// emit requires 边：tool_result → tool_use
				fromNodeID, ok := toolCallIDToNodeID[b.ToolUseID]
				if !ok {
					return nil, nil, fmt.Errorf("proto: anthropic_messages messages[%d] tool_result references unknown tool_use_id=%q", mi, b.ToolUseID)
				}
				edgeSeq++
				env.CapabilityGraph.Edges = append(env.CapabilityGraph.Edges, CapabilityEdge{
					ID:       fmt.Sprintf("e_req_%d", edgeSeq),
					Type:     EdgeRequires,
					From:     nodeID,
					To:       fromNodeID,
					Required: true,
					Reason:   "tool_result requires originating tool_use (INV-19)",
				})
			case "image":
				if b.Source == nil {
					return nil, nil, fmt.Errorf("proto: anthropic_messages messages[%d].content[%d] image missing source", mi, bi)
				}
				imgNode, imgLoss, err := buildAnthropicImageNode(b.Source, mi, bi)
				if err != nil {
					return nil, nil, err
				}
				losses = append(losses, imgLoss...)
				nodeSeq++
				nodeID := fmt.Sprintf("n_image_%d", nodeSeq)
				imgRaw, _ := json.Marshal(b.Source)
				cm.Content = append(cm.Content, CanonicalContentBlock{Type: "image", Image: imgRaw})
				env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
					ID: nodeID, Kind: CapabilityImage, StreamReady: StreamReadyYes,
					Source: &NodeSourceRef{MessageIndex: &msgIdx, BlockIndex: &blkIdx},
					Image:  imgNode,
				})
				env.ProviderProjection.CapabilityResults = append(env.ProviderProjection.CapabilityResults, CapabilityProjection{
					Capability: CapabilityImage, NodeID: nodeID, Verdict: ProjectionPreserved,
				})
				maybeEmitAnthropicCacheControl(env, b.CacheControl, nodeID, mi, bi, &nodeSeq, &edgeSeq)
			case "thinking":
				loss, _ := NewClientLossEntry(ProtocolLossInfo, "anthropic_thinking_block_d1x_pending", "d1x_thinking_block_pending", CapabilityThinking, "")
				losses = append(losses, loss)
			default:
				loss, _ := NewClientLossEntry(ProtocolLossWarning, "anthropic_unknown_block_type:"+b.Type, "unknown_block_type", CapabilityText, "")
				losses = append(losses, loss)
			}
		}
		env.Messages = append(env.Messages, cm)
	}

	// system 也 emit CapabilityText 节点（role=system），方便 graph-level 检索。
	if env.RequestControls.SystemPrompt != "" {
		nodeSeq++
		nodeID := fmt.Sprintf("n_text_%d", nodeSeq)
		env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
			ID:          nodeID,
			Kind:        CapabilityText,
			StreamReady: StreamReadyYes,
			Source:      &NodeSourceRef{RequestField: "system"},
			Text: &TextNode{
				Role:  "system",
				Block: CanonicalContentBlock{Type: "text", Text: env.RequestControls.SystemPrompt},
			},
		})
		env.ProviderProjection.CapabilityResults = append(env.ProviderProjection.CapabilityResults, CapabilityProjection{
			Capability: CapabilityText,
			NodeID:     nodeID,
			Verdict:    ProjectionPreserved,
		})
	}

	// thinking 顶层字段 → CapabilityThinking 节点。
	if len(req.Thinking) > 0 {
		var thinkCfg anthropicThinkingConfig
		if err := json.Unmarshal(req.Thinking, &thinkCfg); err != nil {
			return nil, nil, fmt.Errorf("proto: anthropic_messages 'thinking' parse: %w", err)
		}
		if thinkCfg.Type == "enabled" {
			nodeSeq++
			nodeID := fmt.Sprintf("n_thinking_%d", nodeSeq)
			env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
				ID: nodeID, Kind: CapabilityThinking, StreamReady: StreamReadyPartial,
				Source: &NodeSourceRef{RequestField: "thinking"},
				Thinking: &ThinkingNode{
					BudgetTokens: thinkCfg.BudgetTokens,
					Blocks:       []CanonicalContentBlock{},
					Redaction:    RedactionPublic,
				},
			})
			env.ProviderProjection.CapabilityResults = append(env.ProviderProjection.CapabilityResults, CapabilityProjection{
				Capability: CapabilityThinking, NodeID: nodeID, Verdict: ProjectionPreserved,
			})
		}
	}
	if len(req.Metadata) > 0 {
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "d1_metadata_not_yet_implemented", "d1_pending", "", "")
		losses = append(losses, loss)
	}

	return env, losses, nil
}
