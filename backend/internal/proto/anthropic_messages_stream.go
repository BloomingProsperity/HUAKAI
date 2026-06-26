package proto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// P-2 D3 + D4 anthropic_messages streaming — CanonicalEventToClientChunk +
// FinalizeClientStream + per-stream state。

// AnthropicMessagesStreamState 是 anthropic_messages client adapter 的 per-stream
// 状态；forwarder 在 stream 起点初始化，逐事件传入。
//
// 设计：
//   - Started 标记 message_start 是否已 emit；二次重发被吞掉。
//   - Terminated 标记 message_stop 是否已 emit；FinalizeClientStream 不再重发。
//   - OpenBlocks 记录每个 index 当前是否处于"已 start，未 stop"中间态；
//     FinalizeClientStream 用它合成补 content_block_stop 防止 client SDK hang。
//   - Usage 累积；message_delta 中 output_tokens 走累积值。
type AnthropicMessagesStreamState struct {
	Started    bool
	Terminated bool
	OpenBlocks map[int]bool
	Usage      CanonicalUsage
}

// NewAnthropicMessagesStreamState 构造一个空 state；map 已分配。
func NewAnthropicMessagesStreamState() *AnthropicMessagesStreamState {
	return &AnthropicMessagesStreamState{OpenBlocks: make(map[int]bool)}
}

// anthropicStreamStateRef 把 *AnthropicMessagesStreamState 或 nil 转成统一非 nil
// 引用；state == nil 时返回临时新 state（容忍 forwarder 漏初始化）。
func anthropicStreamStateRef(state any) (*AnthropicMessagesStreamState, error) {
	if state == nil {
		return NewAnthropicMessagesStreamState(), nil
	}
	s, ok := state.(*AnthropicMessagesStreamState)
	if !ok {
		return nil, fmt.Errorf("proto: anthropic_messages stream state type mismatch: %T", state)
	}
	if s.OpenBlocks == nil {
		s.OpenBlocks = make(map[int]bool)
	}
	return s, nil
}

// CanonicalEventToClientChunk 把单条 CanonicalEvent 转成 Anthropic Messages SSE
// chunk。守门：
//   - message_start 之外的事件在 Started=false 时拒绝。
//   - Terminated=true 后所有事件吞掉。
func (a *AnthropicMessagesClient) CanonicalEventToClientChunk(ctx context.Context, canonicalEvt any, state any) ([][]byte, []ProtocolLossEntry, error) {
	s, err := anthropicStreamStateRef(state)
	if err != nil {
		return nil, nil, err
	}
	evt, ok := canonicalEvt.(*CanonicalEvent)
	if !ok || evt == nil {
		return nil, nil, fmt.Errorf("proto: anthropic_messages canonical event expected *CanonicalEvent, got %T", canonicalEvt)
	}

	if s.Terminated {
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "event_after_terminal_dropped:"+evt.Type, "post_terminal_event", "", "")
		return nil, []ProtocolLossEntry{loss}, nil
	}

	switch evt.Type {
	case "message_start":
		if s.Started {
			loss, _ := NewClientLossEntry(ProtocolLossInfo, "duplicate_message_start_dropped", "duplicate_message_start", "", "")
			return nil, []ProtocolLossEntry{loss}, nil
		}
		s.Started = true
		payload := map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            evt.MessageID,
				"type":          "message",
				"role":          "assistant",
				"model":         evt.Model,
				"content":       []any{},
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage": map[string]any{
					"input_tokens":  zeroIfNilInt(evt.Usage, func(u *CanonicalUsage) int { return u.InputTokens }),
					"output_tokens": zeroIfNilInt(evt.Usage, func(u *CanonicalUsage) int { return u.OutputTokens }),
				},
			},
		}
		if evt.Usage != nil {
			s.Usage = *evt.Usage
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, nil, fmt.Errorf("proto: marshal message_start: %w", err)
		}
		return [][]byte{EmitSSEEvent("message_start", body)}, nil, nil

	case "content_block_start":
		if !s.Started {
			return nil, nil, errors.New("proto: anthropic_messages content_block_start before message_start")
		}
		if evt.ContentBlock == nil {
			return nil, nil, errors.New("proto: anthropic_messages content_block_start missing content_block")
		}
		block, blockLoss := renderAnthropicResponseBlockForStart(evt.ContentBlock)
		payload := map[string]any{
			"type":          "content_block_start",
			"index":         evt.Index,
			"content_block": block,
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, nil, fmt.Errorf("proto: marshal content_block_start: %w", err)
		}
		s.OpenBlocks[evt.Index] = true
		out := [][]byte{EmitSSEEvent("content_block_start", body)}
		// gemini 等上游在 start 即携带完整工具入参(ContentBlock.Input,无后续 input_json_delta)。
		// Anthropic 线格式里 content_block_start 的 input 恒为 {}、客户端只从 input_json_delta 累积入参,
		// 因此把 start 携带的 Input 合成一条 input_json_delta 发出,客户端才能拿到入参(否则整条工具入参丢失)。
		// 对 anthropic/openai 上游(start 入参为空、走真 delta),此处 len(Input)==0 不触发,无重复。
		if cb := evt.ContentBlock; (cb.Type == "tool_use" || cb.Type == "server_tool_use") && meaningfulToolInput(cb.Input) {
			deltaPayload := map[string]any{
				"type":  "content_block_delta",
				"index": evt.Index,
				"delta": map[string]any{"type": "input_json_delta", "partial_json": string(cb.Input)},
			}
			if db, derr := json.Marshal(deltaPayload); derr == nil {
				out = append(out, EmitSSEEvent("content_block_delta", db))
			}
		}
		return out, blockLoss, nil

	case "content_block_delta":
		if !s.Started {
			return nil, nil, errors.New("proto: anthropic_messages content_block_delta before message_start")
		}
		if evt.Delta == nil {
			return nil, nil, errors.New("proto: anthropic_messages content_block_delta missing delta")
		}
		delta, deltaLoss := renderAnthropicResponseDelta(evt.Delta)
		if delta == nil {
			return nil, deltaLoss, nil
		}
		payload := map[string]any{
			"type":  "content_block_delta",
			"index": evt.Index,
			"delta": delta,
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, nil, fmt.Errorf("proto: marshal content_block_delta: %w", err)
		}
		return [][]byte{EmitSSEEvent("content_block_delta", body)}, deltaLoss, nil

	case "content_block_stop":
		if !s.Started {
			return nil, nil, errors.New("proto: anthropic_messages content_block_stop before message_start")
		}
		payload := map[string]any{
			"type":  "content_block_stop",
			"index": evt.Index,
		}
		body, _ := json.Marshal(payload)
		delete(s.OpenBlocks, evt.Index)
		return [][]byte{EmitSSEEvent("content_block_stop", body)}, nil, nil

	case "message_delta":
		if !s.Started {
			return nil, nil, errors.New("proto: anthropic_messages message_delta before message_start")
		}
		deltaInner := map[string]any{}
		if evt.StopReason != "" {
			mapped, stopLoss := canonicalToAnthropicStopReason(evt.StopReason)
			if mapped != nil {
				deltaInner["stop_reason"] = *mapped
			} else {
				deltaInner["stop_reason"] = nil
			}
			deltaInner["stop_sequence"] = nil
			payload := map[string]any{
				"type":  "message_delta",
				"delta": deltaInner,
			}
			if evt.Usage != nil {
				s.Usage = *evt.Usage
				payload["usage"] = map[string]any{
					"output_tokens": evt.Usage.OutputTokens,
				}
			}
			body, _ := json.Marshal(payload)
			return [][]byte{EmitSSEEvent("message_delta", body)}, stopLoss, nil
		}
		if evt.Usage != nil {
			s.Usage = *evt.Usage
			payload := map[string]any{
				"type":  "message_delta",
				"delta": map[string]any{},
				"usage": map[string]any{"output_tokens": evt.Usage.OutputTokens},
			}
			body, _ := json.Marshal(payload)
			return [][]byte{EmitSSEEvent("message_delta", body)}, nil, nil
		}
		loss, _ := NewClientLossEntry(ProtocolLossInfo, "empty_message_delta_dropped", "empty_message_delta", "", "")
		return nil, []ProtocolLossEntry{loss}, nil

	case "message_stop":
		if !s.Started {
			return nil, nil, errors.New("proto: anthropic_messages message_stop before message_start")
		}
		s.Terminated = true
		var out [][]byte
		for idx := range s.OpenBlocks {
			stopBody, _ := json.Marshal(map[string]any{"type": "content_block_stop", "index": idx})
			out = append(out, EmitSSEEvent("content_block_stop", stopBody))
		}
		s.OpenBlocks = make(map[int]bool)
		body, _ := json.Marshal(map[string]any{"type": "message_stop"})
		out = append(out, EmitSSEEvent("message_stop", body))
		return out, nil, nil

	case "ping":
		body, _ := json.Marshal(map[string]any{"type": "ping"})
		return [][]byte{EmitSSEEvent("ping", body)}, nil, nil

	case "":
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "empty_canonical_event_type_dropped", "empty_event_type", "", "")
		return nil, []ProtocolLossEntry{loss}, nil

	default:
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "unknown_canonical_event_type:"+evt.Type, "unknown_event_type", "", "")
		return nil, []ProtocolLossEntry{loss}, nil
	}
}

// FinalizeClientStream 在 stream 结束时调用；若没收到 message_stop，合成补
// content_block_stop（per-open-block） + message_stop，确保客户端 SDK 不 hang。
// 已 terminated 时返回 empty。
func (a *AnthropicMessagesClient) FinalizeClientStream(ctx context.Context, state any) ([][]byte, error) {
	s, err := anthropicStreamStateRef(state)
	if err != nil {
		return nil, err
	}
	if s.Terminated || !s.Started {
		return nil, nil
	}
	var out [][]byte
	for idx := range s.OpenBlocks {
		body, _ := json.Marshal(map[string]any{"type": "content_block_stop", "index": idx})
		out = append(out, EmitSSEEvent("content_block_stop", body))
	}
	s.OpenBlocks = make(map[int]bool)
	body, _ := json.Marshal(map[string]any{"type": "message_stop"})
	out = append(out, EmitSSEEvent("message_stop", body))
	s.Terminated = true
	return out, nil
}

// renderAnthropicResponseBlockForStart 把 canonical content block 渲染为
// content_block_start 的 inner block 对象。
func renderAnthropicResponseBlockForStart(b *CanonicalContentBlock) (map[string]any, []ProtocolLossEntry) {
	switch b.Type {
	case "text":
		return map[string]any{"type": "text", "text": ""}, nil
	case "tool_use":
		return map[string]any{
			"type":  "tool_use",
			"id":    b.CallID,
			"name":  b.Name,
			"input": json.RawMessage("{}"),
		}, nil
	case "thinking":
		// Claude 客户端经中转的 extended thinking 回程:start 空文本,文本由
		// thinking_delta 流出(下方 renderAnthropicResponseDelta)。
		return map[string]any{"type": "thinking", "thinking": ""}, nil
	case "redacted_thinking":
		block := map[string]any{"type": "redacted_thinking"}
		if len(b.Data) > 0 {
			block["data"] = b.Data
		}
		return block, nil
	case "server_tool_use":
		return map[string]any{
			"type":  "server_tool_use",
			"id":    b.CallID,
			"name":  b.Name,
			"input": json.RawMessage("{}"),
		}, nil
	default:
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "block_start_type_d1_pending:"+b.Type, "d1_pending_block_start", "", "")
		return map[string]any{"type": b.Type}, []ProtocolLossEntry{loss}
	}
}

// renderAnthropicResponseDelta 把 CanonicalContentDelta 渲染为 Anthropic SSE delta
// inner object。返回 nil delta 表示无可表达字节，调用方仅发 loss。
func renderAnthropicResponseDelta(d *CanonicalContentDelta) (map[string]any, []ProtocolLossEntry) {
	switch d.Type {
	case "text_delta":
		return map[string]any{"type": "text_delta", "text": d.Text}, nil
	case "tool_input_delta", "input_json_delta":
		// 上游 SSE 解析器(anthropic/sse.go、openai/sse.go)把工具入参 delta 统一产出为 canonical
		// 类型 tool_input_delta;此前这里只认 anthropic 线名 input_json_delta = 真 canonical 类型掉
		// default 被丢 → 跨协议流式工具入参整条丢失。两种拼写都接,输出仍为 anthropic 线 input_json_delta。
		partial := d.PartialJSON
		if len(partial) == 0 {
			partial = json.RawMessage(`""`)
		}
		return map[string]any{"type": "input_json_delta", "partial_json": partial}, nil
	case "reasoning_delta", "thinking_delta":
		// 上游 anthropic 适配器把 thinking_delta 统一转成 canonical reasoning_delta
		// (anthropic/sse.go canonicalDelta);此前这里只认 thinking_delta=死分支,
		// 真实到来的 reasoning_delta 掉 default 被丢 = 中转 thinking 文本全丢。
		text := d.ReasoningText
		if text == "" {
			text = d.Text
		}
		return map[string]any{"type": "thinking_delta", "thinking": text}, nil
	case "signature_delta":
		// 上游按 CarryForwardSignatureDelta 策略门决定是否转发;能到这里就该渲染。
		return map[string]any{"type": "signature_delta", "signature": d.Signature}, nil
	default:
		loss, _ := NewClientLossEntry(ProtocolLossWarning, "unknown_delta_type:"+d.Type, "unknown_delta_type", "", "")
		return nil, []ProtocolLossEntry{loss}
	}
}

// zeroIfNilInt 安全取 usage 字段；nil 返回 0。
func zeroIfNilInt(u *CanonicalUsage, get func(*CanonicalUsage) int) int {
	if u == nil {
		return 0
	}
	return get(u)
}
