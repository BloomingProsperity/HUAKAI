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
// D10/D11/D12 stub。

type OpenAIResponsesClient struct{}

var _ ClientAdapter = (*OpenAIResponsesClient)(nil)

type openAIResponsesRequest struct {
	Model              string             `json:"model"`
	Input              json.RawMessage    `json:"input"` // string or array
	Instructions       string             `json:"instructions,omitempty"`
	Stream             *bool              `json:"stream"`
	MaxOutputTokens    *int               `json:"max_output_tokens"`
	Temperature        *float64           `json:"temperature"`
	TopP               *float64           `json:"top_p"`
	Tools              []json.RawMessage  `json:"tools,omitempty"`
	ToolChoice         json.RawMessage    `json:"tool_choice,omitempty"`
	ParallelToolCalls  *bool              `json:"parallel_tool_calls"`
	Text               json.RawMessage    `json:"text,omitempty"`
	Reasoning          json.RawMessage    `json:"reasoning,omitempty"`
	Store              *bool              `json:"store"`
	Metadata           map[string]any     `json:"metadata,omitempty"`
	PreviousResponseID string             `json:"previous_response_id,omitempty"`
}

type openAIResponsesInputItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role,omitempty"`     // message item
	Content   json.RawMessage `json:"content,omitempty"`  // message item
	CallID    string          `json:"call_id,omitempty"`  // function_call / function_call_output
	Name      string          `json:"name,omitempty"`     // function_call
	Arguments string          `json:"arguments,omitempty"`// function_call
	Output    string          `json:"output,omitempty"`   // function_call_output
}

type openAIResponsesInputPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL json.RawMessage `json:"image_url,omitempty"`
	Source   json.RawMessage `json:"source,omitempty"`
	Detail   string          `json:"detail,omitempty"`
}

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

	// Tools: first-class function tools + native_required for built-ins
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

	// Stream
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
				Source: &NodeSourceRef{MessageIndex: &msgIdx, BlockIndex: &blkIdx},
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

func parseOpenAIResponsesInput(raw json.RawMessage) ([]openAIResponsesInputItem, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		// 顶层 input string 视为单一 user message。
		contentBytes, _ := json.Marshal([]openAIResponsesInputPart{{Type: "input_text", Text: s}})
		return []openAIResponsesInputItem{{Type: "message", Role: "user", Content: contentBytes}}, nil
	}
	var items []openAIResponsesInputItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("proto: openai_responses 'input' must be string or array of items: %w", err)
	}
	return items, nil
}

func parseOpenAIResponsesContent(raw json.RawMessage, mi int) ([]string, []ProtocolLossEntry, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []string{s}, nil, nil
	}
	var parts []openAIResponsesInputPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, nil, fmt.Errorf("proto: openai_responses input[%d].content must be string or part array", mi)
	}
	var texts []string
	var losses []ProtocolLossEntry
	for _, p := range parts {
		switch p.Type {
		case "input_text", "output_text", "text":
			texts = append(texts, p.Text)
		case "input_image":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "responses_input_image_d9_pending", "d9_image_pending", CapabilityImage, "")
			losses = append(losses, loss)
		case "input_file":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "responses_input_file_d9_pending", "d9_file_pending", CapabilityFile, "")
			losses = append(losses, loss)
		default:
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "responses_unknown_part:"+p.Type, "unknown_part_type", "", "")
			losses = append(losses, loss)
		}
	}
	return texts, losses, nil
}

// convertOpenAIResponsesTools 把 Responses tools[] 转 CanonicalTool[]。
// function 类型直接转；built-in 类型（web_search / code_interpreter / ...）
// 按 synthesis Q9 决策 D：emit native_required loss + Mandatory Roadmap，不入
// RequestControls.Tools。
func convertOpenAIResponsesTools(tools []json.RawMessage) ([]CanonicalTool, []ProtocolLossEntry, error) {
	var canonicalTools []CanonicalTool
	var losses []ProtocolLossEntry
	for i, rt := range tools {
		var head struct {
			Type        string          `json:"type"`
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
			Function    *struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Parameters  json.RawMessage `json:"parameters"`
			} `json:"function,omitempty"`
		}
		if err := json.Unmarshal(rt, &head); err != nil {
			return nil, nil, fmt.Errorf("proto: openai_responses tools[%d] parse: %w", i, err)
		}
		switch head.Type {
		case "function", "":
			// 新 Responses 形态：name/description/parameters 直接顶层；旧形态 function: {...}。
			name := head.Name
			desc := head.Description
			params := head.Parameters
			if head.Function != nil {
				if name == "" {
					name = head.Function.Name
				}
				if desc == "" {
					desc = head.Function.Description
				}
				if len(params) == 0 {
					params = head.Function.Parameters
				}
			}
			if name == "" {
				return nil, nil, fmt.Errorf("proto: openai_responses tools[%d] function tool missing name", i)
			}
			canonicalTools = append(canonicalTools, CanonicalTool{
				Name:        name,
				Description: desc,
				InputSchema: params,
			})
		case "web_search", "web_search_preview", "code_interpreter", "computer_use_preview", "file_search":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "responses_builtin_tool_native_required:"+head.Type, "builtin_tool_native_required", CapabilityToolUse, "")
			loss.NativePath = "/v1/native/openai/responses"
			loss.Suggestion = "use native passthrough at /v1/native/openai/responses; Mandatory Roadmap for plugin shell"
			losses = append(losses, loss)
		default:
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "responses_unknown_tool_type:"+head.Type, "unknown_tool_type", CapabilityToolUse, "")
			losses = append(losses, loss)
		}
	}
	return canonicalTools, losses, nil
}

// ----------------------------------------------------------------------------
// D10 CanonicalToClientResponse — HCSF buffered → OpenAI Responses JSON
// ----------------------------------------------------------------------------

type openAIResponsesResponse struct {
	ID                string                       `json:"id"`
	Object            string                       `json:"object"`
	Model             string                       `json:"model"`
	Status            string                       `json:"status"`
	IncompleteDetails *openAIResponsesIncomplete   `json:"incomplete_details"`
	Output            []map[string]any             `json:"output"`
	Usage             openAIResponsesUsage         `json:"usage"`
}

type openAIResponsesIncomplete struct {
	Reason string `json:"reason"`
}

type openAIResponsesUsage struct {
	InputTokens         int                                  `json:"input_tokens"`
	OutputTokens        int                                  `json:"output_tokens"`
	TotalTokens         int                                  `json:"total_tokens"`
	InputTokensDetails  *openAIResponsesUsageInputDetails    `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *openAIResponsesUsageOutputDetails   `json:"output_tokens_details,omitempty"`
}

type openAIResponsesUsageInputDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type openAIResponsesUsageOutputDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// canonicalToResponsesStatus 映射 canonical stop → OpenAI Responses status
// + 可选 incomplete_details。OpenAI Responses 用 status="completed"/"incomplete"
// 而不是 finish_reason 字符串。
func canonicalToResponsesStatus(c CanonicalStopReason) (status string, incomplete *openAIResponsesIncomplete, losses []ProtocolLossEntry) {
	switch c {
	case CanonicalStopEndTurn, CanonicalStopSequence, CanonicalStopToolUse:
		return "completed", nil, nil
	case CanonicalStopMaxTokens:
		return "incomplete", &openAIResponsesIncomplete{Reason: "max_output_tokens"}, nil
	case CanonicalStopRefusal:
		return "incomplete", &openAIResponsesIncomplete{Reason: "content_filter"}, nil
	case CanonicalStopUnknown, "":
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "canonical_stop_reason_unknown_for_responses", "stop_reason_unknown", "", "")
		return "completed", nil, []ProtocolLossEntry{loss}
	default:
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "canonical_stop_reason_unmapped:"+string(c), "stop_reason_unmapped", "", "")
		return "completed", nil, []ProtocolLossEntry{loss}
	}
}

// CanonicalToClientResponse 把 HCSF buffered envelope 序列化为 OpenAI Responses
// API response JSON。
func (o *OpenAIResponsesClient) CanonicalToClientResponse(ctx context.Context, canonical *HCSF) ([]byte, []ProtocolLossEntry, error) {
	if canonical == nil {
		return nil, nil, errors.New("proto: openai_responses CanonicalToClientResponse nil envelope")
	}
	if canonical.BufferedResponse == nil {
		return nil, nil, errors.New("proto: openai_responses CanonicalToClientResponse envelope has no buffered_response")
	}
	resp := canonical.BufferedResponse

	var losses []ProtocolLossEntry

	output := make([]map[string]any, 0, len(resp.Content))
	// 文本 block 合并到一个 message item（assistant role）下；tool_use 各自成
	// function_call output item。
	var msgTexts []map[string]any
	for i, b := range resp.Content {
		switch b.Type {
		case "text":
			msgTexts = append(msgTexts, map[string]any{"type": "output_text", "text": b.Text})
		case "tool_use":
			if b.CallID == "" || b.Name == "" {
				return nil, nil, fmt.Errorf("proto: openai_responses content[%d] tool_use missing call_id or name", i)
			}
			args := string(b.Input)
			if args == "" {
				args = "{}"
			}
			output = append(output, map[string]any{
				"type":      "function_call",
				"id":        "fc_" + b.CallID,
				"call_id":   b.CallID,
				"name":      b.Name,
				"arguments": args,
			})
		case "tool_result":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "tool_result_in_assistant_response_dropped", "tool_result_in_response", CapabilityToolResult, "")
			losses = append(losses, loss)
		case "reasoning":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "reasoning_block_d10_pending", "d10_reasoning_pending", CapabilityThinking, "")
			losses = append(losses, loss)
		case "image":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "image_in_response_d10_pending", "d10_image_response_pending", CapabilityImage, "")
			losses = append(losses, loss)
		default:
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "unknown_response_block_type:"+b.Type, "unknown_response_block_type", "", "")
			losses = append(losses, loss)
		}
	}
	if len(msgTexts) > 0 {
		// 把 message item 放在 output 数组前部，function_call 在后部（与 OpenAI 规范一致）。
		msgItem := map[string]any{
			"type":    "message",
			"id":      "msg_" + resp.ID,
			"role":    "assistant",
			"content": msgTexts,
		}
		output = append([]map[string]any{msgItem}, output...)
	}

	status, incomplete, statusLoss := canonicalToResponsesStatus(resp.StopReason)
	losses = append(losses, statusLoss...)

	usage := openAIResponsesUsage{
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
		TotalTokens:  resp.Usage.InputTokens + resp.Usage.OutputTokens,
	}
	if resp.Usage.CacheReadInputTokens > 0 {
		usage.InputTokensDetails = &openAIResponsesUsageInputDetails{CachedTokens: resp.Usage.CacheReadInputTokens}
	}
	if canonical.Accounting.ReasoningTokens > 0 {
		usage.OutputTokensDetails = &openAIResponsesUsageOutputDetails{ReasoningTokens: canonical.Accounting.ReasoningTokens}
	}

	out := openAIResponsesResponse{
		ID:                resp.ID,
		Object:            "response",
		Model:             resp.Model,
		Status:            status,
		IncompleteDetails: incomplete,
		Output:            output,
		Usage:             usage,
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, nil, fmt.Errorf("proto: openai_responses marshal response: %w", err)
	}
	return body, losses, nil
}
// ----------------------------------------------------------------------------
// D11 CanonicalEventToClientChunk — canonical event → OpenAI Responses SSE chunk
// ----------------------------------------------------------------------------

// OpenAIResponsesStreamState 是 openai_responses client adapter 的 per-stream 状态。
// OpenAI Responses 用 named events，且 output_item / content_part 各自有 lifecycle。
// 本片只完整实现 text 路径（output_item=message + content_part=output_text）；
// tool_use 路径 emit warning loss + 不 emit chunk（D11.x 后续）。
//
// 守门：
//   - Started=false 时 inner 事件 reject。
//   - Terminated 后吞所有，避免重发 response.completed。
type OpenAIResponsesStreamState struct {
	ResponseID         string
	Model              string
	Started            bool
	Terminated         bool
	CurrentOutputIndex int
	CurrentItemID      string // message_xxx
	CurrentContentPart int
	ItemOpen           bool
	ContentPartOpen    bool
}

func NewOpenAIResponsesStreamState() *OpenAIResponsesStreamState {
	return &OpenAIResponsesStreamState{CurrentOutputIndex: -1, CurrentContentPart: -1}
}

func openAIResponsesStateRef(state any) (*OpenAIResponsesStreamState, error) {
	if state == nil {
		return NewOpenAIResponsesStreamState(), nil
	}
	s, ok := state.(*OpenAIResponsesStreamState)
	if !ok {
		return nil, fmt.Errorf("proto: openai_responses stream state type mismatch: %T", state)
	}
	return s, nil
}

func (o *OpenAIResponsesClient) CanonicalEventToClientChunk(ctx context.Context, canonicalEvt any, state any) ([][]byte, []ProtocolLossEntry, error) {
	s, err := openAIResponsesStateRef(state)
	if err != nil {
		return nil, nil, err
	}
	evt, ok := canonicalEvt.(*CanonicalEvent)
	if !ok || evt == nil {
		return nil, nil, fmt.Errorf("proto: openai_responses canonical event expected *CanonicalEvent, got %T", canonicalEvt)
	}
	if s.Terminated {
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "event_after_response_completed_dropped:"+evt.Type, "post_completed_event", "", "")
		return nil, []ProtocolLossEntry{loss}, nil
	}

	switch evt.Type {
	case "message_start":
		if s.Started {
			loss, _ := NewClientLossEntry(ProtocolLossInfo, "duplicate_message_start_dropped", "duplicate_message_start", "", "")
			return nil, []ProtocolLossEntry{loss}, nil
		}
		s.Started = true
		s.ResponseID = evt.MessageID
		s.Model = evt.Model
		payload := map[string]any{
			"type": "response.created",
			"response": map[string]any{
				"id":     evt.MessageID,
				"object": "response",
				"model":  evt.Model,
				"status": "in_progress",
				"output": []any{},
			},
		}
		body, _ := json.Marshal(payload)
		return [][]byte{EmitSSEEvent("response.created", body)}, nil, nil

	case "content_block_start":
		if !s.Started {
			return nil, nil, errors.New("proto: openai_responses content_block_start before message_start")
		}
		if evt.ContentBlock == nil {
			return nil, nil, errors.New("proto: openai_responses content_block_start missing content_block")
		}
		switch evt.ContentBlock.Type {
		case "text":
			s.CurrentOutputIndex = evt.Index
			s.CurrentItemID = fmt.Sprintf("msg_%s_%d", s.ResponseID, evt.Index)
			s.ItemOpen = true
			s.CurrentContentPart = 0
			s.ContentPartOpen = true
			var out [][]byte
			added := map[string]any{
				"type":         "response.output_item.added",
				"output_index": evt.Index,
				"item": map[string]any{
					"type":    "message",
					"id":      s.CurrentItemID,
					"role":    "assistant",
					"content": []any{},
				},
			}
			body, _ := json.Marshal(added)
			out = append(out, EmitSSEEvent("response.output_item.added", body))
			partAdded := map[string]any{
				"type":          "response.content_part.added",
				"item_id":       s.CurrentItemID,
				"output_index":  evt.Index,
				"content_index": 0,
				"part":          map[string]any{"type": "output_text", "text": ""},
			}
			body2, _ := json.Marshal(partAdded)
			out = append(out, EmitSSEEvent("response.content_part.added", body2))
			return out, nil, nil
		case "tool_use":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "responses_streaming_tool_use_d11_pending", "d11_tool_use_pending", CapabilityToolUse, "")
			return nil, []ProtocolLossEntry{loss}, nil
		default:
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "responses_unknown_block_start:"+evt.ContentBlock.Type, "unknown_block_start", "", "")
			return nil, []ProtocolLossEntry{loss}, nil
		}

	case "content_block_delta":
		if !s.Started {
			return nil, nil, errors.New("proto: openai_responses content_block_delta before message_start")
		}
		if evt.Delta == nil {
			return nil, nil, errors.New("proto: openai_responses content_block_delta missing delta")
		}
		switch evt.Delta.Type {
		case "text_delta":
			payload := map[string]any{
				"type":          "response.output_text.delta",
				"item_id":       s.CurrentItemID,
				"output_index":  s.CurrentOutputIndex,
				"content_index": s.CurrentContentPart,
				"delta":         evt.Delta.Text,
			}
			body, _ := json.Marshal(payload)
			return [][]byte{EmitSSEEvent("response.output_text.delta", body)}, nil, nil
		case "input_json_delta":
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "responses_streaming_input_json_delta_d11_pending", "d11_tool_args_delta_pending", CapabilityToolUse, "")
			return nil, []ProtocolLossEntry{loss}, nil
		case "thinking_delta", "signature_delta":
			loss, _ := NewClientLossEntry(ProtocolLossInfo, "responses_thinking_delta_d11_pending:"+evt.Delta.Type, "d11_thinking_delta_pending", CapabilityThinking, "")
			return nil, []ProtocolLossEntry{loss}, nil
		default:
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "responses_unknown_delta_type:"+evt.Delta.Type, "unknown_delta_type", "", "")
			return nil, []ProtocolLossEntry{loss}, nil
		}

	case "content_block_stop":
		if !s.Started {
			return nil, nil, errors.New("proto: openai_responses content_block_stop before message_start")
		}
		var out [][]byte
		if s.ContentPartOpen {
			done := map[string]any{
				"type":          "response.content_part.done",
				"item_id":       s.CurrentItemID,
				"output_index":  s.CurrentOutputIndex,
				"content_index": s.CurrentContentPart,
				"part":          map[string]any{"type": "output_text"},
			}
			body, _ := json.Marshal(done)
			out = append(out, EmitSSEEvent("response.content_part.done", body))
			s.ContentPartOpen = false
		}
		if s.ItemOpen {
			done := map[string]any{
				"type":         "response.output_item.done",
				"output_index": s.CurrentOutputIndex,
				"item": map[string]any{
					"type": "message", "id": s.CurrentItemID, "role": "assistant",
				},
			}
			body, _ := json.Marshal(done)
			out = append(out, EmitSSEEvent("response.output_item.done", body))
			s.ItemOpen = false
		}
		return out, nil, nil

	case "message_delta":
		// Responses 没有专门的 message_delta 事件；usage / stop_reason 在 response.completed 内 carry。
		return nil, nil, nil

	case "message_stop":
		if !s.Started {
			return nil, nil, errors.New("proto: openai_responses message_stop before message_start")
		}
		var out [][]byte
		// 补尚未关闭的 content_part / output_item
		if s.ContentPartOpen {
			done := map[string]any{
				"type":          "response.content_part.done",
				"item_id":       s.CurrentItemID,
				"output_index":  s.CurrentOutputIndex,
				"content_index": s.CurrentContentPart,
				"part":          map[string]any{"type": "output_text"},
			}
			body, _ := json.Marshal(done)
			out = append(out, EmitSSEEvent("response.content_part.done", body))
			s.ContentPartOpen = false
		}
		if s.ItemOpen {
			done := map[string]any{
				"type":         "response.output_item.done",
				"output_index": s.CurrentOutputIndex,
				"item":         map[string]any{"type": "message", "id": s.CurrentItemID, "role": "assistant"},
			}
			body, _ := json.Marshal(done)
			out = append(out, EmitSSEEvent("response.output_item.done", body))
			s.ItemOpen = false
		}
		completed := map[string]any{
			"type": "response.completed",
			"response": map[string]any{
				"id":     s.ResponseID,
				"object": "response",
				"model":  s.Model,
				"status": "completed",
			},
		}
		if evt.Usage != nil {
			completed["response"].(map[string]any)["usage"] = map[string]any{
				"input_tokens":  evt.Usage.InputTokens,
				"output_tokens": evt.Usage.OutputTokens,
				"total_tokens":  evt.Usage.InputTokens + evt.Usage.OutputTokens,
			}
		}
		body, _ := json.Marshal(completed)
		out = append(out, EmitSSEEvent("response.completed", body))
		s.Terminated = true
		return out, nil, nil

	case "ping":
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "responses_no_ping_dropped", "no_ping_in_responses", "", "")
		return nil, []ProtocolLossEntry{loss}, nil

	case "":
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "empty_canonical_event_type_dropped", "empty_event_type", "", "")
		return nil, []ProtocolLossEntry{loss}, nil

	default:
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "unknown_canonical_event_type:"+evt.Type, "unknown_event_type", "", "")
		return nil, []ProtocolLossEntry{loss}, nil
	}
}

// FinalizeClientStream 在 stream 结束时补 open content_part / output_item +
// response.completed。Q8 决策 B：不追加 [DONE]。
func (o *OpenAIResponsesClient) FinalizeClientStream(ctx context.Context, state any) ([][]byte, error) {
	s, err := openAIResponsesStateRef(state)
	if err != nil {
		return nil, err
	}
	if s.Terminated || !s.Started {
		return nil, nil
	}
	var out [][]byte
	if s.ContentPartOpen {
		done := map[string]any{
			"type":          "response.content_part.done",
			"item_id":       s.CurrentItemID,
			"output_index":  s.CurrentOutputIndex,
			"content_index": s.CurrentContentPart,
			"part":          map[string]any{"type": "output_text"},
		}
		body, _ := json.Marshal(done)
		out = append(out, EmitSSEEvent("response.content_part.done", body))
		s.ContentPartOpen = false
	}
	if s.ItemOpen {
		done := map[string]any{
			"type":         "response.output_item.done",
			"output_index": s.CurrentOutputIndex,
			"item":         map[string]any{"type": "message", "id": s.CurrentItemID, "role": "assistant"},
		}
		body, _ := json.Marshal(done)
		out = append(out, EmitSSEEvent("response.output_item.done", body))
		s.ItemOpen = false
	}
	completed := map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     s.ResponseID,
			"object": "response",
			"model":  s.Model,
			"status": "completed",
		},
	}
	body, _ := json.Marshal(completed)
	out = append(out, EmitSSEEvent("response.completed", body))
	s.Terminated = true
	return out, nil
}
