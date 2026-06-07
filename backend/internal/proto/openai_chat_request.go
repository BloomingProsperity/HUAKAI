package proto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// P-2 D5 openai_chat ClientAdapter — RequestToCanonical 第一片：text +
// system/developer roles + tool_calls + tool result（role=tool）+ tools 声明 +
// 基础 RequestControls + stream 标志。
//
// 范围（按 P-2 synthesis §5.1）：
//   - 入：OpenAI Chat Completions HTTP body（JSON）
//   - 出：HCSF v0.4 request envelope；含 Messages、CapabilityText/ToolUse/
//     ToolResult 节点 + EdgeRequires（tool_result→tool_use；）。
//   - image_url / input_audio / file 等 multimodal content part 暂 warning loss。
//   - response_format json_schema / reasoning_effort 作为 capability 节点建模，
//     同时保留上游原始请求形态供 passthrough 投影。

// OpenAIChatClient 实现 ClientAdapter；零值可用。
type OpenAIChatClient struct{}

var _ ClientAdapter = (*OpenAIChatClient)(nil)

// RequestToCanonical 把 OpenAI Chat Completions body 解为 HCSF v0.4 request
// envelope。RequestMeta 必填字段从 context RequestMetaSeed 注入。
func (o *OpenAIChatClient) RequestToCanonical(ctx context.Context, raw []byte) (*HCSF, []ProtocolLossEntry, error) {
	seed, ok := RequestMetaSeedFromContext(ctx)
	if !ok {
		return nil, nil, ErrMissingRequestMetaSeed
	}

	var req openAIChatRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, nil, fmt.Errorf("proto: openai_chat parse body: %w", err)
	}
	if req.Model == "" {
		return nil, nil, errors.New("proto: openai_chat missing required field 'model'")
	}
	if len(req.Messages) == 0 {
		return nil, nil, errors.New("proto: openai_chat 'messages' must not be empty")
	}

	env := NewEmptyEnvelope()
	if err := seed.ApplyToRequestMeta(&env.RequestMeta); err != nil {
		return nil, nil, err
	}
	env.RequestMeta.Model = req.Model

	// RequestControls
	if req.MaxCompletionTokens != nil {
		env.RequestControls.MaxTokens = req.MaxCompletionTokens
	} else if req.MaxTokens != nil {
		env.RequestControls.MaxTokens = req.MaxTokens
	}
	env.RequestControls.Temperature = req.Temperature
	env.RequestControls.TopP = req.TopP
	env.RequestControls.ParallelToolCalls = req.ParallelToolCalls
	env.RequestControls.Seed = req.Seed
	if len(req.ToolChoice) > 0 {
		env.RequestControls.ToolChoice = req.ToolChoice
	}
	if len(req.Tools) > 0 {
		tools, err := convertOpenAITools(req.Tools)
		if err != nil {
			return nil, nil, err
		}
		env.RequestControls.Tools = tools
	}
	if len(req.Stop) > 0 {
		stops, err := parseOpenAIStop(req.Stop)
		if err != nil {
			return nil, nil, err
		}
		env.RequestControls.Stop = stops
	}
	if req.Stream != nil && *req.Stream {
		env.StreamPlan.Mode = StreamModeStreaming
	}

	var losses []ProtocolLossEntry

	if len(req.ResponseFormat) > 0 {
		losses = append(losses, applyOpenAIChatResponseFormat(env, req.ResponseFormat)...)
	}
	if req.ReasoningEffort != "" {
		attachRequestPassthroughFields(env, raw, "reasoning_effort")
		budget := effortToBudgetTokens(req.ReasoningEffort, env.RequestControls.MaxTokens)
		env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
			ID:          "n_thinking_request_1",
			Kind:        CapabilityThinking,
			StreamReady: StreamReadyPartial,
			Source:      &NodeSourceRef{RequestField: "reasoning_effort"},
			Thinking: &ThinkingNode{
				BudgetTokens: budget,
				Blocks:       []CanonicalContentBlock{},
				Redaction:    RedactionProviderOnly,
			},
		})
		env.ProviderProjection.CapabilityResults = append(env.ProviderProjection.CapabilityResults, CapabilityProjection{
			Capability: CapabilityThinking, NodeID: "n_thinking_request_1", Verdict: ProjectionPreserved,
		})
		if budget == 0 {
			loss, _ := NewClientLossEntry(ProtocolLossInfo, "openai_chat_reasoning_effort_unrecognized:"+req.ReasoningEffort, "unknown_reasoning_effort", CapabilityThinking, "n_thinking_request_1")
			losses = append(losses, loss)
		}
	}
	if req.User != "" {
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "openai_user_field_d5_pending", "d5_user_field_pending", "", "")
		losses = append(losses, loss)
	}
	if req.Store != nil {
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "openai_store_field_d5_pending", "d5_store_field_pending", CapabilityDataRetention, "")
		losses = append(losses, loss)
	}
	if len(req.Metadata) > 0 {
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "openai_metadata_field_d5_pending", "d5_metadata_field_pending", "", "")
		losses = append(losses, loss)
	}

	// Messages + CapabilityGraph
	toolCallIDToNodeID := make(map[string]string)
	nodeSeq := 0
	edgeSeq := 0
	for mi, m := range req.Messages {
		if m.Role == "" {
			return nil, nil, fmt.Errorf("proto: openai_chat messages[%d] missing role", mi)
		}
		cm := CanonicalMessage{Role: m.Role}

		// role=tool 是 tool_result，专用 tool_call_id + content 字段。
		if m.Role == "tool" {
			if m.ToolCallID == "" {
				return nil, nil, fmt.Errorf("proto: openai_chat messages[%d] role=tool missing tool_call_id", mi)
			}
			texts, contentLoss, err := parseOpenAIChatContent(m.Content, mi)
			if err != nil {
				return nil, nil, err
			}
			losses = append(losses, contentLoss...)
			toolResultText := flattenOpenAIToolResultContent(texts)
			rawResult, _ := json.Marshal([]CanonicalContentBlock{{Type: "text", Text: toolResultText}})
			cm.Content = append(cm.Content, CanonicalContentBlock{
				Type:       "tool_result",
				CallID:     m.ToolCallID,
				ToolResult: rawResult,
			})
			nodeSeq++
			nodeID := fmt.Sprintf("n_tool_result_%d", nodeSeq)
			msgIdx := mi
			blkIdx := 0
			env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
				ID:          nodeID,
				Kind:        CapabilityToolResult,
				StreamReady: StreamReadyYes,
				Source:      &NodeSourceRef{MessageIndex: &msgIdx, BlockIndex: &blkIdx},
				ToolResult: &ToolResultNode{
					ToolCallID: m.ToolCallID,
					Content:    []CanonicalContentBlock{{Type: "text", Text: toolResultText}},
					Status:     ToolNodeComplete,
				},
			})
			env.ProviderProjection.CapabilityResults = append(env.ProviderProjection.CapabilityResults, CapabilityProjection{
				Capability: CapabilityToolResult, NodeID: nodeID, Verdict: ProjectionPreserved,
			})
			fromNodeID, ok := toolCallIDToNodeID[m.ToolCallID]
			if !ok {
				return nil, nil, fmt.Errorf("proto: openai_chat messages[%d] tool_result references unknown tool_call_id=%q", mi, m.ToolCallID)
			}
			edgeSeq++
			env.CapabilityGraph.Edges = append(env.CapabilityGraph.Edges, CapabilityEdge{
				ID: fmt.Sprintf("e_req_%d", edgeSeq), Type: EdgeRequires, From: nodeID, To: fromNodeID, Required: true,
				Reason: "tool_result requires tool_use (INV-19)",
			})
			env.Messages = append(env.Messages, cm)
			continue
		}

		// 通用 role（system / developer / user / assistant）
		texts, contentLoss, err := parseOpenAIChatContent(m.Content, mi)
		if err != nil {
			return nil, nil, err
		}
		losses = append(losses, contentLoss...)
		for bi, t := range texts {
			cm.Content = append(cm.Content, CanonicalContentBlock{Type: "text", Text: t})
			nodeSeq++
			nodeID := fmt.Sprintf("n_text_%d", nodeSeq)
			msgIdx := mi
			blkIdx := bi
			env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
				ID:          nodeID,
				Kind:        CapabilityText,
				StreamReady: StreamReadyYes,
				Source:      &NodeSourceRef{MessageIndex: &msgIdx, BlockIndex: &blkIdx},
				Text:        &TextNode{Role: m.Role, Block: CanonicalContentBlock{Type: "text", Text: t}},
			})
			env.ProviderProjection.CapabilityResults = append(env.ProviderProjection.CapabilityResults, CapabilityProjection{
				Capability: CapabilityText, NodeID: nodeID, Verdict: ProjectionPreserved,
			})
		}

		// assistant role 可能附带 tool_calls
		for tci, tc := range m.ToolCalls {
			if tc.Type != "" && tc.Type != "function" {
				loss, _ := NewClientLossEntry(ProtocolLossWarning, "openai_tool_call_type_unsupported:"+tc.Type, "unsupported_tool_call_type", CapabilityToolUse, "")
				losses = append(losses, loss)
				continue
			}
			if tc.ID == "" || tc.Function.Name == "" {
				return nil, nil, fmt.Errorf("proto: openai_chat messages[%d].tool_calls[%d] missing id or function.name", mi, tci)
			}
			input := json.RawMessage(tc.Function.Arguments)
			if len(input) == 0 || string(input) == "" {
				input = json.RawMessage("{}")
			}
			nodeSeq++
			nodeID := fmt.Sprintf("n_tool_use_%d", nodeSeq)
			msgIdx := mi
			blkIdx := len(cm.Content)
			cm.Content = append(cm.Content, CanonicalContentBlock{
				Type: "tool_use", CallID: tc.ID, Name: tc.Function.Name, Input: input,
			})
			env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
				ID:          nodeID,
				Kind:        CapabilityToolUse,
				StreamReady: StreamReadyPartial,
				Source:      &NodeSourceRef{MessageIndex: &msgIdx, BlockIndex: &blkIdx},
				ToolUse:     &ToolUseNode{ToolCallID: tc.ID, Name: tc.Function.Name, Input: input, Status: ToolNodeComplete},
			})
			env.ProviderProjection.CapabilityResults = append(env.ProviderProjection.CapabilityResults, CapabilityProjection{
				Capability: CapabilityToolUse, NodeID: nodeID, Verdict: ProjectionPreserved,
			})
			toolCallIDToNodeID[tc.ID] = nodeID
		}

		env.Messages = append(env.Messages, cm)
	}

	return env, losses, nil
}

func applyOpenAIChatResponseFormat(env *HCSF, raw json.RawMessage) []ProtocolLossEntry {
	env.RequestControls.ResponseFormat = &ResponseFormat{
		Type:   "raw",
		Schema: append(json.RawMessage(nil), raw...),
	}

	var shape openAIChatResponseFormatShape
	if err := json.Unmarshal(raw, &shape); err != nil {
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "openai_chat_response_format_unmodeled", "response_format_unmodeled", CapabilityStructuredOutput, "")
		return []ProtocolLossEntry{loss}
	}
	if shape.Type != "json_schema" {
		return nil
	}
	if shape.JSONSchema == nil || len(shape.JSONSchema.Schema) == 0 || string(shape.JSONSchema.Schema) == "null" {
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "openai_chat_response_format_json_schema_missing_schema", "response_format_schema_missing", CapabilityStructuredOutput, "")
		return []ProtocolLossEntry{loss}
	}
	if !jsonRawObject(shape.JSONSchema.Schema) {
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "openai_chat_response_format_json_schema_unmodeled_schema", "response_format_schema_unmodeled", CapabilityStructuredOutput, "")
		return []ProtocolLossEntry{loss}
	}

	strict := false
	if shape.JSONSchema.Strict != nil {
		strict = *shape.JSONSchema.Strict
	}
	nodeID := "n_structured_output_1"
	env.CapabilityGraph.Nodes = append(env.CapabilityGraph.Nodes, CapabilityNode{
		ID:          nodeID,
		Kind:        CapabilityStructuredOutput,
		StreamReady: StreamReadyYes,
		Source:      &NodeSourceRef{RequestField: "response_format"},
		StructuredOutput: &StructuredOutputNode{
			Mode:   StructuredOutputJSONSchema,
			Strict: strict,
			Schema: append(json.RawMessage(nil), shape.JSONSchema.Schema...),
		},
	})
	env.ProviderProjection.CapabilityResults = append(env.ProviderProjection.CapabilityResults, CapabilityProjection{
		Capability: CapabilityStructuredOutput, NodeID: nodeID, Verdict: ProjectionPreserved,
	})
	return nil
}

func jsonRawObject(raw json.RawMessage) bool {
	var obj map[string]json.RawMessage
	return json.Unmarshal(raw, &obj) == nil && obj != nil
}

func effortToBudgetTokens(effort string, maxTokens *int) int {
	var budget int
	switch strings.ToLower(effort) {
	case "low":
		budget = 1280
	case "medium":
		budget = 2048
	case "high":
		budget = 4096
	default:
		return 0
	}
	if maxTokens != nil && *maxTokens >= 0 && budget > *maxTokens {
		return *maxTokens
	}
	return budget
}
