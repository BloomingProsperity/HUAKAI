package proto

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

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
	ID            string         // chatcmpl-id（message_start 后定下）
	Model         string         // 模型名
	Started       bool           // role delta 已 emit
	Terminated    bool           // finish_reason chunk 已 emit
	DoneEmitted   bool           // [DONE] 已 emit
	ToolSlotIndex map[string]int // CallID → choices[0].delta.tool_calls[i].index
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

// ForceOpenAIChatChunkFormat optionally fills the minimum OpenAI Chat SSE JSON
// chunk keys expected by strict clients. force=false returns an exact copy.
func ForceOpenAIChatChunkFormat(raw []byte, force bool) ([]byte, error) {
	if !force {
		return append([]byte(nil), raw...), nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("[DONE]")) {
		return append([]byte(nil), raw...), nil
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	if root == nil {
		return nil, errors.New("proto: openai_chat chunk must be a JSON object")
	}
	if object, ok := root["object"].(string); !ok || object == "" {
		root["object"] = "chat.completion.chunk"
	}
	choices, ok := root["choices"].([]any)
	if !ok || len(choices) == 0 {
		root["choices"] = []any{
			map[string]any{
				"index":         0,
				"delta":         map[string]any{},
				"finish_reason": nil,
			},
		}
		return json.Marshal(root)
	}
	for i := range choices {
		choice, ok := choices[i].(map[string]any)
		if !ok || choice == nil {
			choice = map[string]any{}
			choices[i] = choice
		}
		if _, ok := choice["index"]; !ok {
			choice["index"] = i
		}
	}
	root["choices"] = choices
	return json.Marshal(root)
}

func (o *OpenAIChatClient) formatChunk(ctx context.Context, raw []byte) []byte {
	force := o != nil && o.ForceFormat
	if seed, ok := RequestMetaSeedFromContext(ctx); ok && seed.ForceFormat {
		force = true
	}
	out, err := ForceOpenAIChatChunkFormat(raw, force)
	if err != nil {
		return raw
	}
	return out
}

func (o *OpenAIChatClient) marshalChunk(ctx context.Context, chunk map[string]any) []byte {
	body, _ := json.Marshal(chunk)
	return o.formatChunk(ctx, body)
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
		body := o.marshalChunk(ctx, chunk)
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
			body := o.marshalChunk(ctx, chunk)
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
			body := o.marshalChunk(ctx, chunk)
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
			body := o.marshalChunk(ctx, chunk)
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
		if evt.NativeFinishReason != "" {
			choice["native_finish_reason"] = evt.NativeFinishReason
		}
		chunk := s.openAIChunkBase()
		chunk["choices"] = []any{choice}
		body := o.marshalChunk(ctx, chunk)
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
		body := o.marshalChunk(ctx, chunk)
		out = append(out, EmitSSEDataLine(body))
		s.Terminated = true
	}
	out = append(out, EmitSSEDone())
	s.DoneEmitted = true
	return out, nil
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
