package proto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

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
