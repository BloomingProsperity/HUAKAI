package proto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// OpenAIResponsesStreamState 是 openai_responses client adapter 的 per-stream 状态。
// OpenAI Responses 用 named events，且 output_item / content_part 各自有 lifecycle。
// 本片实现 text 路径（output_item=message + content_part=output_text）与
// function_call 路径（output_item=function_call + function_call_arguments.delta/done，
// 形状对齐 buffered 渲染器 openai_responses_response.go）。此前 tool_use 只记
// warning loss 不 emit = 跨协议流式中转（如 Codex CLI 走 Claude 池）丢全部
// function call，agent 循环静默断裂。
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

	// function_call item 生命周期（D11 tool 路）
	ToolItemOpen bool
	ToolCallID   string
	ToolName     string
	ToolArgs     []byte

	// reasoning item 生命周期（thinking/reasoning 块 → Responses reasoning output item）
	ReasoningItemOpen bool
	ReasoningItemID   string
	ReasoningText     []byte
	ReasoningSig      string
}

func NewOpenAIResponsesStreamState() *OpenAIResponsesStreamState {
	return &OpenAIResponsesStreamState{CurrentOutputIndex: -1, CurrentContentPart: -1}
}

// closeOpenMessageItem 收尾未关闭的 message item（content_part.done + output_item.done）。
// content_block_stop / message_stop / Finalize 共用，保证三处事件形状一致。
func (s *OpenAIResponsesStreamState) closeOpenMessageItem() [][]byte {
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
	return out
}

// closeOpenReasoningItem 收尾 reasoning item：output_item.done(type reasoning，
// summary=[{summary_text}]，签名进 encrypted_content)，形状对齐 buffered 渲染器
// openAIResponsesReasoningOutputItem。
func (s *OpenAIResponsesStreamState) closeOpenReasoningItem() [][]byte {
	if !s.ReasoningItemOpen {
		return nil
	}
	summary := make([]map[string]string, 0, 1)
	if len(s.ReasoningText) > 0 {
		summary = append(summary, map[string]string{"type": "summary_text", "text": string(s.ReasoningText)})
	}
	item := map[string]any{
		"type":    "reasoning",
		"id":      s.ReasoningItemID,
		"status":  "completed",
		"summary": summary,
	}
	if s.ReasoningSig != "" {
		item["encrypted_content"] = s.ReasoningSig
	}
	done := map[string]any{
		"type":         "response.output_item.done",
		"output_index": s.CurrentOutputIndex,
		"item":         item,
	}
	body, _ := json.Marshal(done)
	s.ReasoningItemOpen = false
	s.ReasoningText = nil
	s.ReasoningSig = ""
	return [][]byte{EmitSSEEvent("response.output_item.done", body)}
}

// closeOpenToolItem 收尾 function_call item：arguments.done + output_item.done，
// arguments 为流上累积的完整入参（空则 {}，对齐 buffered 渲染器）。
func (s *OpenAIResponsesStreamState) closeOpenToolItem() [][]byte {
	if !s.ToolItemOpen {
		return nil
	}
	args := string(s.ToolArgs)
	if args == "" {
		args = "{}"
	}
	var out [][]byte
	argsDone := map[string]any{
		"type":         "response.function_call_arguments.done",
		"item_id":      s.CurrentItemID,
		"output_index": s.CurrentOutputIndex,
		"arguments":    args,
	}
	b1, _ := json.Marshal(argsDone)
	out = append(out, EmitSSEEvent("response.function_call_arguments.done", b1))
	itemDone := map[string]any{
		"type":         "response.output_item.done",
		"output_index": s.CurrentOutputIndex,
		"item": map[string]any{
			"type":      "function_call",
			"id":        s.CurrentItemID,
			"call_id":   s.ToolCallID,
			"name":      s.ToolName,
			"arguments": args,
		},
	}
	b2, _ := json.Marshal(itemDone)
	out = append(out, EmitSSEEvent("response.output_item.done", b2))
	s.ToolItemOpen = false
	s.ToolArgs = nil
	return out
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
			var out [][]byte
			out = append(out, s.closeOpenToolItem()...) // 防御：上块未 stop 先补收尾
			out = append(out, s.closeOpenReasoningItem()...)
			s.CurrentOutputIndex = evt.Index
			s.CurrentItemID = fmt.Sprintf("msg_%s_%d", s.ResponseID, evt.Index)
			s.ItemOpen = true
			s.CurrentContentPart = 0
			s.ContentPartOpen = true
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
			if evt.ContentBlock.CallID == "" || evt.ContentBlock.Name == "" {
				loss, _ := NewClientLossEntry(ProtocolLossWarning, "responses_streaming_tool_use_missing_call_id_or_name", "tool_use_missing_identity", CapabilityToolUse, "")
				return nil, []ProtocolLossEntry{loss}, nil
			}
			var out [][]byte
			out = append(out, s.closeOpenMessageItem()...) // 防御：上块未 stop 先补收尾
			s.CurrentOutputIndex = evt.Index
			s.CurrentItemID = "fc_" + evt.ContentBlock.CallID
			s.ToolItemOpen = true
			s.ToolCallID = evt.ContentBlock.CallID
			s.ToolName = evt.ContentBlock.Name
			s.ToolArgs = nil
			// gemini 等上游在 start 即携带完整入参（无后续 delta），直接累积；anthropic 等上游 start
			// 发占位 `{}`、真入参由 input_json_delta 流上累积。用 meaningfulToolInput 排除占位 `{}`/null,
			// 否则会把 `{}` 与后续真 delta 双发拼成损坏 JSON(对抗审查抓出的同类回归,一并收紧)。
			if meaningfulToolInput(evt.ContentBlock.Input) {
				s.ToolArgs = append(s.ToolArgs, evt.ContentBlock.Input...)
			}
			added := map[string]any{
				"type":         "response.output_item.added",
				"output_index": evt.Index,
				"item": map[string]any{
					"type":      "function_call",
					"id":        s.CurrentItemID,
					"call_id":   s.ToolCallID,
					"name":      s.ToolName,
					"arguments": "",
				},
			}
			body, _ := json.Marshal(added)
			out = append(out, EmitSSEEvent("response.output_item.added", body))
			return out, nil, nil
		case "thinking", "reasoning", "redacted_thinking":
			// 上游 thinking 块 → Responses reasoning output item(buffered 路早已渲染
			// reasoning item;此前流式掉 default 当 unknown 丢 = 流式/缓冲不对称)。
			var out [][]byte
			out = append(out, s.closeOpenToolItem()...)
			out = append(out, s.closeOpenMessageItem()...)
			s.CurrentOutputIndex = evt.Index
			s.ReasoningItemOpen = true
			s.ReasoningItemID = fmt.Sprintf("rs_%s_%d", s.ResponseID, evt.Index)
			s.ReasoningText = nil
			s.ReasoningSig = evt.ContentBlock.Signature
			if evt.ContentBlock.Thinking != "" {
				s.ReasoningText = append(s.ReasoningText, evt.ContentBlock.Thinking...)
			}
			added := map[string]any{
				"type":         "response.output_item.added",
				"output_index": evt.Index,
				"item": map[string]any{
					"type":    "reasoning",
					"id":      s.ReasoningItemID,
					"summary": []any{},
				},
			}
			body, _ := json.Marshal(added)
			out = append(out, EmitSSEEvent("response.output_item.added", body))
			return out, nil, nil
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
		case "tool_input_delta", "input_json_delta":
			// 上游解析器统一产出 canonical 类型 tool_input_delta;此前只认 input_json_delta → 跨协议
			// 流式工具入参 delta 掉 default 被丢。两种拼写都接,输出仍为 Responses function_call_arguments.delta。
			if !s.ToolItemOpen {
				loss, _ := NewClientLossEntry(ProtocolLossWarning, "input_json_delta_without_open_function_call", "tool_args_delta_no_item", CapabilityToolUse, "")
				return nil, []ProtocolLossEntry{loss}, nil
			}
			s.ToolArgs = append(s.ToolArgs, evt.Delta.PartialJSON...)
			payload := map[string]any{
				"type":         "response.function_call_arguments.delta",
				"item_id":      s.CurrentItemID,
				"output_index": s.CurrentOutputIndex,
				"delta":        string(evt.Delta.PartialJSON),
			}
			body, _ := json.Marshal(payload)
			return [][]byte{EmitSSEEvent("response.function_call_arguments.delta", body)}, nil, nil
		case "reasoning_delta", "thinking_delta":
			// 上游统一发 canonical reasoning_delta(thinking_delta 兼容留存);此前
			// reasoning_delta 掉 default 当 unknown 丢 = 流式 reasoning 摘要全丢。
			if !s.ReasoningItemOpen {
				loss, _ := NewClientLossEntry(ProtocolLossInfo, "reasoning_delta_without_open_item", "reasoning_delta_no_item", CapabilityThinking, "")
				return nil, []ProtocolLossEntry{loss}, nil
			}
			text := evt.Delta.ReasoningText
			if text == "" {
				text = evt.Delta.Text
			}
			s.ReasoningText = append(s.ReasoningText, text...)
			payload := map[string]any{
				"type":          "response.reasoning_summary_text.delta",
				"item_id":       s.ReasoningItemID,
				"output_index":  s.CurrentOutputIndex,
				"summary_index": 0,
				"delta":         text,
			}
			body, _ := json.Marshal(payload)
			return [][]byte{EmitSSEEvent("response.reasoning_summary_text.delta", body)}, nil, nil
		case "signature_delta":
			if s.ReasoningItemOpen {
				s.ReasoningSig += evt.Delta.Signature
				return nil, nil, nil
			}
			loss, _ := NewClientLossEntry(ProtocolLossInfo, "signature_delta_without_open_item", "signature_delta_no_item", CapabilityThinking, "")
			return nil, []ProtocolLossEntry{loss}, nil
		default:
			loss, _ := NewClientLossEntry(ProtocolLossWarning, "responses_unknown_delta_type:"+evt.Delta.Type, "unknown_delta_type", "", "")
			return nil, []ProtocolLossEntry{loss}, nil
		}

	case "content_block_stop":
		if !s.Started {
			return nil, nil, errors.New("proto: openai_responses content_block_stop before message_start")
		}
		if s.ToolItemOpen {
			return s.closeOpenToolItem(), nil, nil
		}
		if s.ReasoningItemOpen {
			return s.closeOpenReasoningItem(), nil, nil
		}
		return s.closeOpenMessageItem(), nil, nil

	case "message_delta":
		// Responses 没有专门的 message_delta 事件；usage / stop_reason 在 response.completed 内 carry。
		return nil, nil, nil

	case "message_stop":
		if !s.Started {
			return nil, nil, errors.New("proto: openai_responses message_stop before message_start")
		}
		var out [][]byte
		// 补尚未关闭的 function_call / content_part / output_item
		out = append(out, s.closeOpenToolItem()...)
		out = append(out, s.closeOpenReasoningItem()...)
		out = append(out, s.closeOpenMessageItem()...)
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
	out = append(out, s.closeOpenToolItem()...)
	out = append(out, s.closeOpenReasoningItem()...)
	out = append(out, s.closeOpenMessageItem()...)
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
