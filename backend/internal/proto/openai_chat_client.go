package proto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// P-2 D5 openai_chat ClientAdapter — RequestToCanonical 第一片：text +
// system/developer roles + tool_calls + tool result（role=tool）+ tools 声明 +
// 基础 RequestControls + stream 标志。
//
// 范围（按 P-2 synthesis §5.1）：
//   - 入：OpenAI Chat Completions HTTP body（JSON）
//   - 出：HCSF v0.4 request envelope；含 Messages、CapabilityText/ToolUse/
//     ToolResult 节点 + EdgeRequires（tool_result→tool_use；INV-19）。
//   - image_url / input_audio / file 等 multimodal content part 暂 warning loss。
//   - reasoning_effort / response_format json_schema 在 D5.x 后续小片。
//
// D6/D7/D8 hookpoint stub 返回 ErrNotImplemented。

// OpenAIChatClient 实现 ClientAdapter；零值可用。
type OpenAIChatClient struct{}

var _ ClientAdapter = (*OpenAIChatClient)(nil)

type openAIChatRequest struct {
	Model               string             `json:"model"`
	Messages            []openAIChatMsg    `json:"messages"`
	Stream              *bool              `json:"stream"`
	MaxTokens           *int               `json:"max_tokens"`
	MaxCompletionTokens *int               `json:"max_completion_tokens"`
	Temperature         *float64           `json:"temperature"`
	TopP                *float64           `json:"top_p"`
	Stop                json.RawMessage    `json:"stop,omitempty"` // string 或 []string
	Tools               []openAIChatTool   `json:"tools,omitempty"`
	ToolChoice          json.RawMessage    `json:"tool_choice,omitempty"`
	ParallelToolCalls   *bool              `json:"parallel_tool_calls"`
	ResponseFormat      json.RawMessage    `json:"response_format,omitempty"`
	Seed                *int               `json:"seed"`
	User                string             `json:"user,omitempty"`
	Store               *bool              `json:"store"`
	Metadata            map[string]any     `json:"metadata,omitempty"`
}

type openAIChatMsg struct {
	Role       string              `json:"role"`
	Content    json.RawMessage     `json:"content"`
	Name       string              `json:"name,omitempty"`
	ToolCalls  []openAIChatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string              `json:"tool_call_id,omitempty"` // role=tool 用
}

type openAIChatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON-encoded string
	} `json:"function"`
}

type openAIChatTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type openAIChatContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL json.RawMessage `json:"image_url,omitempty"`
}

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
		// D5.x 待补：json_schema 解析 + StructuredOutputNode；D5 first slice 保留 raw。
		rf := &ResponseFormat{Type: "raw", Schema: req.ResponseFormat}
		env.RequestControls.ResponseFormat = rf
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "response_format_d5_raw_passthrough", "d5_response_format_raw", CapabilityStructuredOutput, "")
		losses = append(losses, loss)
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

// parseOpenAIChatContent 解 message.content 字段：string 视为单一 text；array
// 解 content parts。
func parseOpenAIChatContent(raw json.RawMessage, mi int) ([]string, []ProtocolLossEntry, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []string{s}, nil, nil
	}
	var parts []openAIChatContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, nil, fmt.Errorf("proto: openai_chat messages[%d].content must be string or part array", mi)
	}
	var texts []string
	var losses []ProtocolLossEntry
	for _, p := range parts {
		switch p.Type {
		case "text":
			texts = append(texts, p.Text)
		case "image_url":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "openai_image_url_d5_pending", "d5_image_pending", CapabilityImage, "")
			losses = append(losses, loss)
		case "input_audio":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "openai_input_audio_d5_pending", "d5_audio_pending", CapabilityAudio, "")
			losses = append(losses, loss)
		case "file":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "openai_file_part_d5_pending", "d5_file_part_pending", CapabilityFile, "")
			losses = append(losses, loss)
		default:
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "openai_unknown_content_part:"+p.Type, "unknown_content_part", CapabilityText, "")
			losses = append(losses, loss)
		}
	}
	return texts, losses, nil
}

// convertOpenAITools 把 OpenAI tools[] 转 CanonicalTool[]。
func convertOpenAITools(tools []openAIChatTool) ([]CanonicalTool, error) {
	out := make([]CanonicalTool, 0, len(tools))
	for i, t := range tools {
		if t.Type != "" && t.Type != "function" {
			return nil, fmt.Errorf("proto: openai_chat tools[%d] unsupported type %q (only 'function' in D5)", i, t.Type)
		}
		if t.Function.Name == "" {
			return nil, fmt.Errorf("proto: openai_chat tools[%d] missing function.name", i)
		}
		out = append(out, CanonicalTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
	}
	return out, nil
}

// parseOpenAIStop 解 stop 字段：string 或 []string。
func parseOpenAIStop(raw json.RawMessage) ([]string, error) {
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, nil
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("proto: openai_chat 'stop' must be string or array of strings: %w", err)
	}
	return arr, nil
}

// flattenOpenAIToolResultContent 把 tool message 的 text parts 拼成单串。
func flattenOpenAIToolResultContent(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += "\n" + p
	}
	return out
}

// ----------------------------------------------------------------------------
// D6 CanonicalToClientResponse — HCSF buffered → OpenAI Chat completion JSON
// ----------------------------------------------------------------------------

type openAIChatCompletion struct {
	ID      string                  `json:"id"`
	Object  string                  `json:"object"`
	Created int64                   `json:"created,omitempty"`
	Model   string                  `json:"model"`
	Choices []openAIChatChoice      `json:"choices"`
	Usage   openAIChatResponseUsage `json:"usage"`
}

type openAIChatChoice struct {
	Index        int                   `json:"index"`
	Message      openAIChatChoiceMsg   `json:"message"`
	FinishReason *string               `json:"finish_reason"`
}

type openAIChatChoiceMsg struct {
	Role      string                   `json:"role"`
	Content   *string                  `json:"content"` // null when tool_calls present
	ToolCalls []openAIChatResponseToolCall `json:"tool_calls,omitempty"`
}

type openAIChatResponseToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function openAIChatResponseToolCallFunc `json:"function"`
}

type openAIChatResponseToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAIChatResponseUsage struct {
	PromptTokens     int                            `json:"prompt_tokens"`
	CompletionTokens int                            `json:"completion_tokens"`
	TotalTokens      int                            `json:"total_tokens"`
	PromptDetails    *openAIChatUsagePromptDetails `json:"prompt_tokens_details,omitempty"`
}

type openAIChatUsagePromptDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

func canonicalToOpenAIFinishReason(c CanonicalStopReason) (*string, []ProtocolLossEntry) {
	switch c {
	case CanonicalStopEndTurn, CanonicalStopSequence:
		s := "stop"
		return &s, nil
	case CanonicalStopMaxTokens:
		s := "length"
		return &s, nil
	case CanonicalStopToolUse:
		s := "tool_calls"
		return &s, nil
	case CanonicalStopRefusal:
		s := "content_filter"
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "canonical_refusal_mapped_to_content_filter", "refusal_to_content_filter", "", "")
		return &s, []ProtocolLossEntry{loss}
	case CanonicalStopUnknown, "":
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "canonical_stop_reason_unknown", "stop_reason_unknown", "", "")
		return nil, []ProtocolLossEntry{loss}
	default:
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "canonical_stop_reason_unmapped:"+string(c), "stop_reason_unmapped", "", "")
		return nil, []ProtocolLossEntry{loss}
	}
}

// CanonicalToClientResponse 把 HCSF buffered envelope 序列化为 OpenAI Chat
// completion JSON。
func (o *OpenAIChatClient) CanonicalToClientResponse(ctx context.Context, canonical *HCSF) ([]byte, []ProtocolLossEntry, error) {
	if canonical == nil {
		return nil, nil, errors.New("proto: openai_chat CanonicalToClientResponse nil envelope")
	}
	if canonical.BufferedResponse == nil {
		return nil, nil, errors.New("proto: openai_chat CanonicalToClientResponse envelope has no buffered_response")
	}
	resp := canonical.BufferedResponse

	var losses []ProtocolLossEntry

	// 拼 message.content / tool_calls
	var textParts []string
	var toolCalls []openAIChatResponseToolCall
	for i, b := range resp.Content {
		switch b.Type {
		case "text":
			textParts = append(textParts, b.Text)
		case "tool_use":
			if b.CallID == "" || b.Name == "" {
				return nil, nil, fmt.Errorf("proto: openai_chat content[%d] tool_use missing call_id or name", i)
			}
			args := string(b.Input)
			if args == "" {
				args = "{}"
			}
			toolCalls = append(toolCalls, openAIChatResponseToolCall{
				ID:   b.CallID,
				Type: "function",
				Function: openAIChatResponseToolCallFunc{
					Name:      b.Name,
					Arguments: args,
				},
			})
		case "tool_result":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "tool_result_in_assistant_response_dropped", "tool_result_in_response", CapabilityToolResult, "")
			losses = append(losses, loss)
		case "reasoning":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "reasoning_block_d5_pending", "d5_reasoning_pending", CapabilityThinking, "")
			losses = append(losses, loss)
		case "image":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "image_in_response_d5_pending", "d5_image_response_pending", CapabilityImage, "")
			losses = append(losses, loss)
		default:
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "unknown_response_block_type:"+b.Type, "unknown_response_block_type", "", "")
			losses = append(losses, loss)
		}
	}

	msg := openAIChatChoiceMsg{Role: "assistant"}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
		// OpenAI 规范：有 tool_calls 时 content 可以是 null。
		if len(textParts) > 0 {
			joined := joinNonEmpty(textParts, "\n")
			msg.Content = &joined
		}
	} else {
		joined := joinNonEmpty(textParts, "\n")
		msg.Content = &joined
	}

	finish, stopLoss := canonicalToOpenAIFinishReason(resp.StopReason)
	losses = append(losses, stopLoss...)

	usage := openAIChatResponseUsage{
		PromptTokens:     resp.Usage.InputTokens,
		CompletionTokens: resp.Usage.OutputTokens,
		TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
	}
	if resp.Usage.CacheReadInputTokens > 0 {
		usage.PromptDetails = &openAIChatUsagePromptDetails{CachedTokens: resp.Usage.CacheReadInputTokens}
	}

	out := openAIChatCompletion{
		ID:      resp.ID,
		Object:  "chat.completion",
		Model:   resp.Model,
		Choices: []openAIChatChoice{{Index: 0, Message: msg, FinishReason: finish}},
		Usage:   usage,
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, nil, fmt.Errorf("proto: openai_chat marshal response: %w", err)
	}
	return body, losses, nil
}

// joinNonEmpty 是 strings.Join 的轻量替代；空 part 跳过。
func joinNonEmpty(parts []string, sep string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	out := parts[0]
	for _, p := range parts[1:] {
		if p == "" {
			continue
		}
		out += sep + p
	}
	return out
}
// ----------------------------------------------------------------------------
// D7 CanonicalEventToClientChunk — canonical event → OpenAI Chat SSE chunk
// ----------------------------------------------------------------------------

// OpenAIChatStreamState 是 openai_chat client adapter 的 per-stream 状态。
// OpenAI Chat SSE 不像 Anthropic 有 content_block lifecycle，只有 role delta +
// content/tool_calls delta + finish_reason chunk + 终止哨兵 [DONE]。
//
// 守门：
//   - Started=false 时 content_block_start 等"内部"事件直接吞掉（不像 Anthropic
//     强制 message_start 先到，OpenAI 流没有这个边界事件）。
//   - Terminated 之后所有事件吞掉。
//   - DoneEmitted 标记 [DONE] 是否已发；Finalize 不重发。
type OpenAIChatStreamState struct {
	ID            string                  // chatcmpl-id（message_start 后定下）
	Model         string                  // 模型名
	Started       bool                    // role delta 已 emit
	Terminated    bool                    // finish_reason chunk 已 emit
	DoneEmitted   bool                    // [DONE] 已 emit
	ToolSlotIndex map[string]int          // CallID → choices[0].delta.tool_calls[i].index
}

// NewOpenAIChatStreamState 构造一个空 state。
func NewOpenAIChatStreamState() *OpenAIChatStreamState {
	return &OpenAIChatStreamState{ToolSlotIndex: make(map[string]int)}
}

func openAIChatStreamStateRef(state any) (*OpenAIChatStreamState, error) {
	if state == nil {
		return NewOpenAIChatStreamState(), nil
	}
	s, ok := state.(*OpenAIChatStreamState)
	if !ok {
		return nil, fmt.Errorf("proto: openai_chat stream state type mismatch: %T", state)
	}
	if s.ToolSlotIndex == nil {
		s.ToolSlotIndex = make(map[string]int)
	}
	return s, nil
}

// openAIChunkBase 构造一个最小 chat.completion.chunk 骨架。
func (s *OpenAIChatStreamState) openAIChunkBase() map[string]any {
	return map[string]any{
		"id":      s.ID,
		"object":  "chat.completion.chunk",
		"model":   s.Model,
		"choices": []any{},
	}
}

// CanonicalEventToClientChunk 把 CanonicalEvent 转 OpenAI Chat SSE chunk。
func (o *OpenAIChatClient) CanonicalEventToClientChunk(ctx context.Context, canonicalEvt any, state any) ([][]byte, []ProtocolLossEntry, error) {
	s, err := openAIChatStreamStateRef(state)
	if err != nil {
		return nil, nil, err
	}
	evt, ok := canonicalEvt.(*CanonicalEvent)
	if !ok || evt == nil {
		return nil, nil, fmt.Errorf("proto: openai_chat canonical event expected *CanonicalEvent, got %T", canonicalEvt)
	}
	if s.DoneEmitted {
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "event_after_done_dropped:"+evt.Type, "post_done_event", "", "")
		return nil, []ProtocolLossEntry{loss}, nil
	}

	switch evt.Type {
	case "message_start":
		if s.Started {
			loss, _ := NewClientLossEntry(ProtocolLossInfo, "duplicate_message_start_dropped", "duplicate_message_start", "", "")
			return nil, []ProtocolLossEntry{loss}, nil
		}
		s.Started = true
		s.ID = evt.MessageID
		s.Model = evt.Model
		chunk := s.openAIChunkBase()
		chunk["choices"] = []any{
			map[string]any{
				"index":         0,
				"delta":         map[string]any{"role": "assistant"},
				"finish_reason": nil,
			},
		}
		body, _ := json.Marshal(chunk)
		return [][]byte{EmitSSEDataLine(body)}, nil, nil

	case "content_block_start":
		// OpenAI 不需要 block start chunk，但若是 tool_use 需要 emit id+name+空参 的初始 tool_calls delta。
		if evt.ContentBlock == nil {
			return nil, nil, errors.New("proto: openai_chat content_block_start missing content_block")
		}
		switch evt.ContentBlock.Type {
		case "text":
			return nil, nil, nil
		case "tool_use":
			if evt.ContentBlock.CallID == "" || evt.ContentBlock.Name == "" {
				return nil, nil, errors.New("proto: openai_chat tool_use content_block missing call_id/name")
			}
			slot := len(s.ToolSlotIndex)
			s.ToolSlotIndex[evt.ContentBlock.CallID] = slot
			chunk := s.openAIChunkBase()
			chunk["choices"] = []any{
				map[string]any{
					"index": 0,
					"delta": map[string]any{
						"tool_calls": []any{
							map[string]any{
								"index": slot,
								"id":    evt.ContentBlock.CallID,
								"type":  "function",
								"function": map[string]any{
									"name":      evt.ContentBlock.Name,
									"arguments": "",
								},
							},
						},
					},
					"finish_reason": nil,
				},
			}
			body, _ := json.Marshal(chunk)
			return [][]byte{EmitSSEDataLine(body)}, nil, nil
		default:
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "openai_block_start_type_d7_pending:"+evt.ContentBlock.Type, "d7_pending_block_start", "", "")
			return nil, []ProtocolLossEntry{loss}, nil
		}

	case "content_block_delta":
		if evt.Delta == nil {
			return nil, nil, errors.New("proto: openai_chat content_block_delta missing delta")
		}
		switch evt.Delta.Type {
		case "text_delta":
			chunk := s.openAIChunkBase()
			chunk["choices"] = []any{
				map[string]any{
					"index":         0,
					"delta":         map[string]any{"content": evt.Delta.Text},
					"finish_reason": nil,
				},
			}
			body, _ := json.Marshal(chunk)
			return [][]byte{EmitSSEDataLine(body)}, nil, nil
		case "input_json_delta":
			// 找 tool slot：评 evt.Index → 寻 reverse map（OpenAI partial 累积按 tool slot index）。
			slot := evt.Index
			partial := string(evt.Delta.PartialJSON)
			if partial == "" {
				partial = "{}"
			}
			chunk := s.openAIChunkBase()
			chunk["choices"] = []any{
				map[string]any{
					"index": 0,
					"delta": map[string]any{
						"tool_calls": []any{
							map[string]any{
								"index":    slot,
								"function": map[string]any{"arguments": partial},
							},
						},
					},
					"finish_reason": nil,
				},
			}
			body, _ := json.Marshal(chunk)
			return [][]byte{EmitSSEDataLine(body)}, nil, nil
		case "thinking_delta", "signature_delta":
			loss, _ := NewClientLossEntry(ProtocolLossInfo, "openai_chat_no_thinking_channel_dropped:"+evt.Delta.Type, "thinking_in_chat", CapabilityThinking, "")
			return nil, []ProtocolLossEntry{loss}, nil
		default:
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "openai_chat_unknown_delta_type:"+evt.Delta.Type, "unknown_delta_type", "", "")
			return nil, []ProtocolLossEntry{loss}, nil
		}

	case "content_block_stop":
		// OpenAI 不需要 block stop chunk。
		return nil, nil, nil

	case "message_delta":
		if evt.StopReason == "" {
			return nil, nil, nil
		}
		finish, stopLoss := canonicalToOpenAIFinishReason(evt.StopReason)
		choice := map[string]any{
			"index":         0,
			"delta":         map[string]any{},
			"finish_reason": nil,
		}
		if finish != nil {
			choice["finish_reason"] = *finish
		}
		chunk := s.openAIChunkBase()
		chunk["choices"] = []any{choice}
		body, _ := json.Marshal(chunk)
		s.Terminated = true
		return [][]byte{EmitSSEDataLine(body)}, stopLoss, nil

	case "message_stop":
		s.Terminated = true
		s.DoneEmitted = true
		return [][]byte{EmitSSEDone()}, nil, nil

	case "ping":
		// OpenAI Chat 流没有 ping 概念；info-level loss + 不发字节。
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "openai_chat_no_ping_dropped", "no_ping_in_chat", "", "")
		return nil, []ProtocolLossEntry{loss}, nil

	case "":
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "empty_canonical_event_type_dropped", "empty_event_type", "", "")
		return nil, []ProtocolLossEntry{loss}, nil

	default:
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "unknown_canonical_event_type:"+evt.Type, "unknown_event_type", "", "")
		return nil, []ProtocolLossEntry{loss}, nil
	}
}

// FinalizeClientStream 在 stream 结束时调用；若 finish_reason chunk 已发但
// [DONE] 还没发，补 [DONE]；都未发时同时补 finish=stop + [DONE]。
// Idempotent：二次调用 no-op。
func (o *OpenAIChatClient) FinalizeClientStream(ctx context.Context, state any) ([][]byte, error) {
	s, err := openAIChatStreamStateRef(state)
	if err != nil {
		return nil, err
	}
	if s.DoneEmitted {
		return nil, nil
	}
	if !s.Started {
		return nil, nil
	}
	var out [][]byte
	if !s.Terminated {
		stop := "stop"
		chunk := s.openAIChunkBase()
		chunk["choices"] = []any{
			map[string]any{
				"index":         0,
				"delta":         map[string]any{},
				"finish_reason": stop,
			},
		}
		body, _ := json.Marshal(chunk)
		out = append(out, EmitSSEDataLine(body))
		s.Terminated = true
	}
	out = append(out, EmitSSEDone())
	s.DoneEmitted = true
	return out, nil
}
