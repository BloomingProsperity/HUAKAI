package proto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// P-2 D9 openai_responses ClientAdapter — RequestToCanonical 第一片：
// input string + message items（含 input_text）+ function_call items +
// function_call_output items + instructions + function tools；built-in tools
// （web_search/code_interpreter 等）按 synthesis Q9 决策 D 走 native_required
// + Mandatory Roadmap loss。
//

type OpenAIResponsesClient struct{}

var _ ClientAdapter = (*OpenAIResponsesClient)(nil)

func (o *OpenAIResponsesClient) RequestToCanonical(ctx context.Context, raw []byte) (*HCSF, []ProtocolLossEntry, error) {
	seed, ok := RequestMetaSeedFromContext(ctx)
	if !ok {
		return nil, nil, ErrMissingRequestMetaSeed
	}
	var req openAIResponsesRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, nil, fmt.Errorf("proto: openai_responses parse body: %w", err)
	}
	if req.Model == "" {
		return nil, nil, errors.New("proto: openai_responses missing required field 'model'")
	}
	if len(req.Input) == 0 || string(req.Input) == "null" {
		return nil, nil, errors.New("proto: openai_responses missing required field 'input'")
	}

	env := NewEmptyEnvelope()
	if err := seed.ApplyToRequestMeta(&env.RequestMeta); err != nil {
		return nil, nil, err
	}
	env.RequestMeta.Model = req.Model
	if req.PreviousResponseID != "" {
		env.RequestMeta.SessionHash = req.PreviousResponseID
	}

	// RequestControls
	if req.MaxOutputTokens != nil {
		env.RequestControls.MaxTokens = req.MaxOutputTokens
	}
	env.RequestControls.Temperature = req.Temperature
	env.RequestControls.TopP = req.TopP
	env.RequestControls.ParallelToolCalls = req.ParallelToolCalls
	if req.Instructions != "" {
		env.RequestControls.SystemPrompt = req.Instructions
	}
	if len(req.ToolChoice) > 0 {
		env.RequestControls.ToolChoice = req.ToolChoice
	}

	var losses []ProtocolLossEntry
	attachRequestPassthroughFields(env, raw,
		"top_logprobs",
		"max_tool_calls",
		"include",
		"conversation",
		"context_management",
		"prompt_cache_key",
		"prompt_cache_retention",
		"truncation",
		"user",
		"enable_thinking",
		"preset",
	)

	// Tools：一等公民 function 工具 + 内置工具走 native_required
	if len(req.Tools) > 0 {
		tools, toolLosses, err := convertOpenAIResponsesTools(req.Tools)
		if err != nil {
			return nil, nil, err
		}
		env.RequestControls.Tools = tools
		losses = append(losses, toolLosses...)
	}

	// text.format → ResponseFormat (json_schema 等)
	if len(req.Text) > 0 {
		env.RequestControls.ResponseFormat = &ResponseFormat{Type: "raw", Schema: req.Text}
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "responses_text_format_d9_raw_passthrough", "d9_text_format_raw", CapabilityStructuredOutput, "")
		losses = append(losses, loss)
	}

	// reasoning effort/summary → 暂作 info loss
	if len(req.Reasoning) > 0 {
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "responses_reasoning_d9_pending", "d9_reasoning_pending", CapabilityThinking, "")
		losses = append(losses, loss)
	}

	if req.Store != nil {
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "responses_store_d9_pending", "d9_store_field_pending", CapabilityDataRetention, "")
		losses = append(losses, loss)
	}
	if len(req.Metadata) > 0 {
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "responses_metadata_d9_pending", "d9_metadata_pending", "", "")
		losses = append(losses, loss)
	}

	// Stream 标志
	if req.Stream != nil && *req.Stream {
		env.StreamPlan.Mode = StreamModeStreaming
	}

	// Instructions → emit system text node 让 graph 完整
	nodeSeq := 0
	if env.RequestControls.SystemPrompt != "" {
		nodeSeq++
		nodeID := fmt.Sprintf("n_text_%d", nodeSeq)
		env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
			ID: nodeID, Kind: CapabilityText, StreamReady: StreamReadyYes,
			Source: &NodeSourceRef{RequestField: "instructions"},
			Text:   &TextNode{Role: "system", Block: CanonicalContentBlock{Type: "text", Text: env.RequestControls.SystemPrompt}},
		})
		env.ProviderProjection.CapabilityResults = append(env.ProviderProjection.CapabilityResults, CapabilityProjection{
			Capability: CapabilityText, NodeID: nodeID, Verdict: ProjectionPreserved,
		})
	}

	// 解 input：string 或 array of items
	items, err := parseOpenAIResponsesInput(req.Input)
	if err != nil {
		return nil, nil, err
	}

	toolCallIDToNodeID := make(map[string]string)
	edgeSeq := 0
	for mi, it := range items {
		switch it.Type {
		case "message":
			if it.Role == "" {
				return nil, nil, fmt.Errorf("proto: openai_responses input[%d] message missing role", mi)
			}
			texts, partLoss, err := parseOpenAIResponsesContent(it.Content, mi)
			if err != nil {
				return nil, nil, err
			}
			losses = append(losses, partLoss...)
			cm := CanonicalMessage{Role: it.Role}
			for bi, t := range texts {
				cm.Content = append(cm.Content, CanonicalContentBlock{Type: "text", Text: t})
				nodeSeq++
				nodeID := fmt.Sprintf("n_text_%d", nodeSeq)
				msgIdx := mi
				blkIdx := bi
				env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
					ID: nodeID, Kind: CapabilityText, StreamReady: StreamReadyYes,
					Source: &NodeSourceRef{MessageIndex: &msgIdx, BlockIndex: &blkIdx},
					Text:   &TextNode{Role: it.Role, Block: CanonicalContentBlock{Type: "text", Text: t}},
				})
				env.ProviderProjection.CapabilityResults = append(env.ProviderProjection.CapabilityResults, CapabilityProjection{
					Capability: CapabilityText, NodeID: nodeID, Verdict: ProjectionPreserved,
				})
			}
			env.Messages = append(env.Messages, cm)
		case "reasoning":
			blocks, redaction, err := openAIResponsesReasoningInputBlocks(it, mi)
			if err != nil {
				return nil, nil, err
			}
			if len(blocks) > 0 {
				env.Messages = append(env.Messages, CanonicalMessage{Role: "assistant", Content: blocks})
			}
			nodeSeq++
			nodeID := fmt.Sprintf("n_thinking_%d", nodeSeq)
			msgIdx := mi
			env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
				ID:          nodeID,
				Kind:        CapabilityThinking,
				StreamReady: StreamReadyPartial,
				Source:      &NodeSourceRef{MessageIndex: &msgIdx},
				Thinking: &ThinkingNode{
					Blocks:    blocks,
					Signature: it.EncryptedContent,
					Redaction: redaction,
				},
			})
			env.ProviderProjection.CapabilityResults = append(env.ProviderProjection.CapabilityResults, CapabilityProjection{
				Capability: CapabilityThinking, NodeID: nodeID, Verdict: ProjectionPreserved,
			})
		case "function_call":
			if it.CallID == "" || it.Name == "" {
				return nil, nil, fmt.Errorf("proto: openai_responses input[%d] function_call missing call_id or name", mi)
			}
			args := json.RawMessage(it.Arguments)
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			cm := CanonicalMessage{Role: "assistant", Content: []CanonicalContentBlock{{
				Type: "tool_use", CallID: it.CallID, Name: it.Name, Input: args,
			}}}
			nodeSeq++
			nodeID := fmt.Sprintf("n_tool_use_%d", nodeSeq)
			msgIdx := mi
			blkIdx := 0
			env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
				ID: nodeID, Kind: CapabilityToolUse, StreamReady: StreamReadyPartial,
				Source:  &NodeSourceRef{MessageIndex: &msgIdx, BlockIndex: &blkIdx},
				ToolUse: &ToolUseNode{ToolCallID: it.CallID, Name: it.Name, Input: args, Status: ToolNodeComplete},
			})
			env.ProviderProjection.CapabilityResults = append(env.ProviderProjection.CapabilityResults, CapabilityProjection{
				Capability: CapabilityToolUse, NodeID: nodeID, Verdict: ProjectionPreserved,
			})
			toolCallIDToNodeID[it.CallID] = nodeID
			env.Messages = append(env.Messages, cm)
		case "function_call_output":
			if it.CallID == "" {
				return nil, nil, fmt.Errorf("proto: openai_responses input[%d] function_call_output missing call_id", mi)
			}
			rawResult, _ := json.Marshal([]CanonicalContentBlock{{Type: "text", Text: it.Output}})
			cm := CanonicalMessage{Role: "tool", Content: []CanonicalContentBlock{{
				Type: "tool_result", CallID: it.CallID, ToolResult: rawResult,
			}}}
			nodeSeq++
			nodeID := fmt.Sprintf("n_tool_result_%d", nodeSeq)
			msgIdx := mi
			blkIdx := 0
			env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
				ID: nodeID, Kind: CapabilityToolResult, StreamReady: StreamReadyYes,
				Source: &NodeSourceRef{MessageIndex: &msgIdx, BlockIndex: &blkIdx},
				ToolResult: &ToolResultNode{
					ToolCallID: it.CallID,
					Content:    []CanonicalContentBlock{{Type: "text", Text: it.Output}},
					Status:     ToolNodeComplete,
				},
			})
			env.ProviderProjection.CapabilityResults = append(env.ProviderProjection.CapabilityResults, CapabilityProjection{
				Capability: CapabilityToolResult, NodeID: nodeID, Verdict: ProjectionPreserved,
			})
			fromNodeID, ok := toolCallIDToNodeID[it.CallID]
			if !ok {
				return nil, nil, fmt.Errorf("proto: openai_responses input[%d] function_call_output references unknown call_id=%q", mi, it.CallID)
			}
			edgeSeq++
			env.CapabilityGraph.Edges = append(env.CapabilityGraph.Edges, CapabilityEdge{
				ID: fmt.Sprintf("e_req_%d", edgeSeq), Type: EdgeRequires, From: nodeID, To: fromNodeID, Required: true,
				Reason: "function_call_output requires function_call (INV-19)",
			})
			env.Messages = append(env.Messages, cm)
		default:
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "responses_unknown_input_item:"+it.Type, "unknown_input_item", "", "")
			losses = append(losses, loss)
		}
	}

	return env, losses, nil
}

func openAIResponsesReasoningInputBlocks(it openAIResponsesInputItem, itemIndex int) ([]CanonicalContentBlock, RedactionClass, error) {
	parts := make([]openAIResponsesReasoningPart, 0, len(it.Summary))
	parts = append(parts, it.Summary...)
	if len(it.Content) > 0 {
		var content []openAIResponsesReasoningPart
		if err := json.Unmarshal(it.Content, &content); err != nil {
			return nil, "", fmt.Errorf("proto: openai_responses input[%d] reasoning content parse: %w", itemIndex, err)
		}
		parts = append(parts, content...)
	}

	blocks := make([]CanonicalContentBlock, 0, len(parts))
	for _, part := range parts {
		if part.Text == "" {
			continue
		}
		blocks = append(blocks, CanonicalContentBlock{
			Type:      "thinking",
			Text:      part.Text,
			Thinking:  part.Text,
			Signature: it.EncryptedContent,
		})
	}
	if len(blocks) == 0 && it.EncryptedContent != "" {
		blocks = append(blocks, CanonicalContentBlock{Type: "thinking", Signature: it.EncryptedContent})
	}
	if len(blocks) == 0 || firstNonEmptyString(blocks[0].Text, blocks[0].Thinking, blocks[0].ReasoningSummary) == "" {
		return blocks, RedactionProviderOnly, nil
	}
	return blocks, RedactionPublic, nil
}
