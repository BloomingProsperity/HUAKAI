package proto

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ----------------------------------------------------------------------------
// D7 CanonicalEventToClientChunk — canonical event → OpenAI Chat SSE chunk 转换
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

// NewClientStreamState 创建当前协议一次流式响应独占的状态。
func (*OpenAIChatClient) NewClientStreamState() any {
	return NewOpenAIChatStreamState()
}

// meaningfulToolInput 判定 content_block_start 携带的工具 Input 是否是"真入参"(需在 start 即投递),
// 而非占位空对象。**关键区分**:gemini 上游在 start 携带完整真入参(无后续 delta)→ true,必须投递;
// 而 Anthropic 流式协议在 start 恒发占位 `"input":{}`、真入参随后由 input_json_delta 流入 → 必须返回
// false,否则会把 `{}` 当真入参与后续 delta 双发,拼成 `{}{...}` 损坏 JSON。空/null 同样不投递。
func meaningfulToolInput(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || bytes.Equal(t, []byte("null")) {
		return false
	}
	// 解析判空,对所有空白/换行变体鲁棒:`{}`、`{ }`、`{\n}` 均解析成空对象 = 占位,不投递
	//(canonical Input 是 json.RawMessage 逐字保留上游字节、不归一化,故字面量比较会漏判带空白的空对象)。
	var obj map[string]json.RawMessage
	if json.Unmarshal(t, &obj) == nil {
		return len(obj) > 0
	}
	// 非对象(数组/标量)罕见——工具入参按契约是对象;保守视为有内容,不静默丢弃。
	return true
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

// ForceOpenAIChatChunkFormat 可选地补齐严格客户端期望的最小 OpenAI Chat
// SSE JSON chunk 键。force=false 时返回原样副本。
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
			// gemini 等上游在 start 即携带完整工具入参(ContentBlock.Input,无后续 delta)。OpenAI 客户端
			// 跨 chunk 拼接 function.arguments,故把 start 携带的入参直接放进首个 chunk 的 arguments=正确;
			// anthropic/openai 上游 start 入参为空(走 delta),此处为 "" 不变。此前恒写 "" → 忽略 Input →
			// gemini→openai_chat 跨协议流式工具入参整条丢失。
			startArgs := ""
			if meaningfulToolInput(evt.ContentBlock.Input) {
				startArgs = string(evt.ContentBlock.Input)
			}
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
									"arguments": startArgs,
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
		case "tool_input_delta", "input_json_delta":
			// 上游解析器统一产出 canonical 类型 tool_input_delta;此前只认 input_json_delta → 跨协议
			// 流式工具入参 delta 掉 default 被丢。两种拼写都接,输出仍为 OpenAI function.arguments 增量。
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
