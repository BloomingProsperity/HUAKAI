package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// Adapter 将 Ollama /api/chat（NDJSON 流式 / 单 JSON 非流式）转换为
// proto.HCSF 事件。
//
// 流式 wire 是 NDJSON：scanner 逐行交付裸 JSON 帧（无 "data:" 前缀、无
// [DONE] 哨兵），done:true 终帧表示流自然结束并携带 usage 计数。
type Adapter struct{}

// SSEEvent 是扫描器可传入的最小事件形态（与其它 proto 子包对齐）。
type SSEEvent struct {
	Type string `json:"type,omitempty"`
	Data []byte `json:"data,omitempty"`
	Done bool   `json:"done,omitempty"`
}

// StreamEnd 是调用方用于表示上游连接自然关闭的哨兵。
type StreamEnd struct{}

// UpstreamState 累积 Ollama 流式响应的跨帧状态。
type UpstreamState struct {
	Model                string
	MessageStarted       bool
	TextBlockOpen        bool
	TextBlockIndex       int
	NextBlockIndex       int
	Terminated           bool
	AccumulatedUsage     proto.CanonicalUsage
	UsageEmitted         bool
	LastStopReason       proto.CanonicalStopReason
	RawDoneReason        string
	GeneratedToolCallSeq int
	// AccountID / PrefixHash / TenantID：forwarder 注入（与其它 upstream
	// state 的注入约定对齐；per-account 指标与跨租户隔离用）。
	AccountID  int64
	PrefixHash string
	TenantID   int64
}

// CanonicalToProviderRequest 委托 MarshalChatRequest；marshal 期间新增的
// graph loss 同步作为返回值损耗列表。
func (a *Adapter) CanonicalToProviderRequest(ctx context.Context, canonical *proto.HCSF) ([]byte, []proto.ProtocolLossEntry, error) {
	_ = ctx
	if canonical == nil {
		return nil, nil, errors.New("ollama: nil HCSF envelope")
	}
	before := len(canonical.CapabilityGraph.ProtocolLoss)
	body, err := MarshalChatRequest(canonical)
	losses := append([]proto.ProtocolLossEntry(nil), canonical.CapabilityGraph.ProtocolLoss[before:]...)
	return body, losses, err
}

// ProviderResponseToCanonical 解析非流式单 JSON 响应。返回带 Version +
// BufferedResponse 的最小 envelope（RequestMeta 由 forwarder 层注入）。
func (a *Adapter) ProviderResponseToCanonical(ctx context.Context, raw []byte) (*proto.HCSF, []proto.ProtocolLossEntry, error) {
	_ = ctx
	var resp chatResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, nil, fmt.Errorf("ollama: malformed chat response: %w", err)
	}
	// 与流式同款 fail-loud:200 + {"error":...} 体不可静默当空响应。
	if resp.Error != "" {
		return nil, nil, fmt.Errorf("ollama: upstream error response (model=%q): %s", resp.Model, resp.Error)
	}
	var losses []proto.ProtocolLossEntry
	buffered := proto.CanonicalResponse{
		Model:      resp.Model,
		Usage:      usageToCanonical(resp),
		StopReason: mapDoneReason(resp.DoneReason),
	}
	losses = append(losses, doneReasonLoss(resp.DoneReason)...)
	if resp.Message != nil {
		if resp.Message.Thinking != "" {
			buffered.Content = append(buffered.Content, proto.CanonicalContentBlock{Type: "thinking", Thinking: resp.Message.Thinking})
		}
		if resp.Message.Content != "" {
			buffered.Content = append(buffered.Content, proto.CanonicalContentBlock{Type: "text", Text: resp.Message.Content})
		}
		seq := 0
		for _, call := range resp.Message.ToolCalls {
			seq++
			buffered.Content = append(buffered.Content, proto.CanonicalContentBlock{
				Type:   "tool_use",
				CallID: syntheticCallID(seq),
				Name:   call.Function.Name,
				Input:  normalizeArgumentsObject(call.Function.Arguments),
			})
		}
	}
	env := &proto.HCSF{
		Version:          proto.HCSFVersion,
		BufferedResponse: &buffered,
	}
	return env, losses, nil
}

// ProviderEventToCanonicalEvents 处理 NDJSON 单帧（scanner 已逐行切好）。
func (a *Adapter) ProviderEventToCanonicalEvents(ctx context.Context, providerEvt any, state any) ([]any, []proto.ProtocolLossEntry, error) {
	_ = ctx
	st, ok := state.(*UpstreamState)
	if !ok {
		return nil, nil, fmt.Errorf("proto: expected *ollama.UpstreamState")
	}
	data, streamEnd, err := coerceFrameData(providerEvt)
	if err != nil {
		return nil, nil, err
	}
	if streamEnd {
		return eventsToAny(finalizeState(st)), nil, nil
	}
	payload := strings.TrimSpace(string(data))
	if payload == "" {
		return nil, nil, nil
	}
	// Ollama NDJSON 无 [DONE] 哨兵；容忍代理层补发的哨兵，按流结束处理。
	if payload == "[DONE]" {
		return eventsToAny(finalizeState(st)), nil, nil
	}

	var frame chatResponse
	if err := json.Unmarshal([]byte(payload), &frame); err != nil {
		loss := proto.NewLossEntry(proto.FeatureTextStreaming, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "malformed Ollama NDJSON frame skipped")
		return nil, []proto.ProtocolLossEntry{loss}, nil
	}
	// fail loud:Ollama 在 HTTP 200 提交后用单行 {"error":"..."} 报中流
	// 致命错误。吞掉它=静默截断伪装成正常终止——客户端拿到干净
	// message_stop 而真相是上游崩了,且 forwarder 的错误收尾/计费证据链
	// 整体被旁路。
	if frame.Error != "" {
		return nil, nil, fmt.Errorf("ollama: upstream stream error frame (model=%q): %s", frame.Model, frame.Error)
	}
	return frameToCanonicalEvents(frame, st)
}

// FinalizeUpstreamStream 在上游 EOF 无 done:true 终帧时补齐终止事件。
// done 帧已置 Terminated 时本函数零输出（双触发去重）。
func (a *Adapter) FinalizeUpstreamStream(ctx context.Context, state any) ([]any, error) {
	_ = ctx
	st, ok := state.(*UpstreamState)
	if !ok {
		return nil, fmt.Errorf("proto: expected *ollama.UpstreamState")
	}
	return eventsToAny(finalizeState(st)), nil
}

func frameToCanonicalEvents(frame chatResponse, st *UpstreamState) ([]any, []proto.ProtocolLossEntry, error) {
	// post-terminal 守卫：done:true 之后的任何帧不得再进 canonical 流
	//（message_stop 已发，继续发 delta 会破坏客户端事件序）；丢弃并记账。
	if st.Terminated {
		loss := proto.NewLossEntry(proto.FeatureTextStreaming, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "ollama stream frame after done:true skipped")
		return nil, []proto.ProtocolLossEntry{loss}, nil
	}
	if frame.Model != "" {
		st.Model = frame.Model
	}

	var events []proto.CanonicalEvent
	var losses []proto.ProtocolLossEntry
	if frame.Message != nil {
		// thinking 增量 → reasoning_delta：思维链非答案正文，不计入计费正文累积。
		if frame.Message.Thinking != "" {
			events = append(events, ensureMessageStart(st)...)
			events = append(events, ensureTextBlock(st)...)
			events = append(events, proto.CanonicalEvent{
				Type:  "content_block_delta",
				Index: st.TextBlockIndex,
				Delta: &proto.CanonicalContentDelta{Type: "reasoning_delta", ReasoningText: frame.Message.Thinking},
			})
		}
		if frame.Message.Content != "" {
			events = append(events, ensureMessageStart(st)...)
			events = append(events, ensureTextBlock(st)...)
			events = append(events, proto.CanonicalEvent{
				Type:  "content_block_delta",
				Index: st.TextBlockIndex,
				Delta: &proto.CanonicalContentDelta{Type: "text_delta", Text: frame.Message.Content},
			})
		}
		// tool_calls 在单帧内完整到达（无增量拼接）：发完整 tool_use 块，
		// arguments 对象直接序列化为 Input。
		for _, call := range frame.Message.ToolCalls {
			events = append(events, ensureMessageStart(st)...)
			events = appendOpenBlockStop(events, st)
			index := st.NextBlockIndex
			st.NextBlockIndex++
			st.GeneratedToolCallSeq++
			block := proto.CanonicalContentBlock{
				Type:   "tool_use",
				CallID: syntheticCallID(st.GeneratedToolCallSeq),
				Name:   call.Function.Name,
				Input:  normalizeArgumentsObject(call.Function.Arguments),
			}
			events = append(events,
				proto.CanonicalEvent{Type: "content_block_start", Index: index, ContentBlock: &block},
				proto.CanonicalEvent{Type: "content_block_stop", Index: index},
			)
		}
	}

	if !frame.Done {
		return eventsToAny(events), losses, nil
	}

	// done:true 终帧：usage（prompt_eval_count→Input, eval_count→Output）+
	// done_reason 映射 + 块收尾 + message_delta + message_stop。
	if usage := usageToCanonical(frame); proto.UsageHasValue(usage) {
		st.AccumulatedUsage = usage
	}
	st.RawDoneReason = frame.DoneReason
	st.LastStopReason = mapDoneReason(frame.DoneReason)
	losses = append(losses, doneReasonLoss(frame.DoneReason)...)

	events = append(events, ensureMessageStart(st)...)
	events = appendOpenBlockStop(events, st)
	// usage 缺失时不发零 usage（message_delta.Usage 留 nil），交 Finalize 兜底。
	var usage *proto.CanonicalUsage
	if proto.UsageHasValue(st.AccumulatedUsage) {
		value := st.AccumulatedUsage
		usage = &value
		st.UsageEmitted = true
	}
	events = append(events,
		proto.CanonicalEvent{Type: "message_delta", Usage: usage, StopReason: st.LastStopReason, NativeFinishReason: st.RawDoneReason},
		proto.CanonicalEvent{Type: "message_stop"},
	)
	st.Terminated = true
	return eventsToAny(events), losses, nil
}

func ensureMessageStart(st *UpstreamState) []proto.CanonicalEvent {
	if st.MessageStarted {
		return nil
	}
	st.MessageStarted = true
	return []proto.CanonicalEvent{{
		Type:  "message_start",
		Model: st.Model,
	}}
}

func ensureTextBlock(st *UpstreamState) []proto.CanonicalEvent {
	if st.TextBlockOpen {
		return nil
	}
	st.TextBlockIndex = st.NextBlockIndex
	st.NextBlockIndex++
	st.TextBlockOpen = true
	block := proto.CanonicalContentBlock{Type: "text"}
	return []proto.CanonicalEvent{{
		Type:         "content_block_start",
		Index:        st.TextBlockIndex,
		ContentBlock: &block,
	}}
}

func appendOpenBlockStop(events []proto.CanonicalEvent, st *UpstreamState) []proto.CanonicalEvent {
	if st.TextBlockOpen {
		events = append(events, proto.CanonicalEvent{Type: "content_block_stop", Index: st.TextBlockIndex})
		st.TextBlockOpen = false
	}
	return events
}

// finalizeState 补齐未终止流的收尾事件；Terminated 标志保证与 done:true
// 终帧双触发时只发一次。
func finalizeState(st *UpstreamState) []proto.CanonicalEvent {
	if st.Terminated {
		return nil
	}
	var events []proto.CanonicalEvent
	events = appendOpenBlockStop(events, st)
	if proto.UsageHasValue(st.AccumulatedUsage) && !st.UsageEmitted {
		usage := st.AccumulatedUsage
		events = append(events, proto.CanonicalEvent{Type: "message_delta", Usage: &usage, StopReason: st.LastStopReason, NativeFinishReason: st.RawDoneReason})
		st.UsageEmitted = true
	}
	st.Terminated = true
	events = append(events, proto.CanonicalEvent{Type: "message_stop"})
	return events
}

// usageToCanonical 映射终帧计数：prompt_eval_count→InputTokens，
// eval_count→OutputTokens，Total=两者之和（Ollama 无 total 字段）。
func usageToCanonical(resp chatResponse) proto.CanonicalUsage {
	out := proto.CanonicalUsage{
		InputTokens:  resp.PromptEvalCount,
		OutputTokens: resp.EvalCount,
	}
	out.TotalTokens = out.InputTokens + out.OutputTokens
	return out
}

// mapDoneReason 映射 done_reason → canonical stop reason：
// stop→end_turn，length→max_tokens，其余（load/unload/…）→ unknown（记 loss）。
func mapDoneReason(reason string) proto.CanonicalStopReason {
	switch reason {
	case "", "stop":
		return proto.CanonicalStopEndTurn
	case "length":
		return proto.CanonicalStopMaxTokens
	default:
		return proto.CanonicalStopUnknown
	}
}

func doneReasonLoss(reason string) []proto.ProtocolLossEntry {
	if reason == "" || mapDoneReason(reason) != proto.CanonicalStopUnknown {
		return nil
	}
	return []proto.ProtocolLossEntry{proto.NewLossEntry(proto.FeatureMaxTokensFinishReason, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "unknown Ollama done_reason mapped to canonical unknown")}
}

// syntheticCallID 为 Ollama tool_calls 合成 canonical call id（原生协议帧
// 不携带调用 id，无可透传源）。
func syntheticCallID(seq int) string {
	return fmt.Sprintf("call_%08x", seq)
}

// coerceFrameData 容忍多种调用方事件形态：[]byte / string / json.RawMessage /
// SSEEvent / 流结束哨兵 / io.EOF。NDJSON 帧本体就是裸 JSON，无前缀可剥。
func coerceFrameData(v any) ([]byte, bool, error) {
	if v == nil {
		return nil, true, nil
	}
	switch evt := v.(type) {
	case StreamEnd:
		return nil, true, nil
	case *StreamEnd:
		return nil, true, nil
	case SSEEvent:
		if evt.Done || evt.Type == "end" {
			return nil, true, nil
		}
		return bytes.TrimSpace(evt.Data), false, nil
	case json.RawMessage:
		return bytes.TrimSpace(evt), false, nil
	case []byte:
		return bytes.TrimSpace(evt), false, nil
	case string:
		return bytes.TrimSpace([]byte(evt)), false, nil
	case error:
		if errors.Is(evt, io.EOF) {
			return nil, true, nil
		}
		return nil, false, evt
	default:
		return nil, false, fmt.Errorf("proto: expected SSEEvent, []byte, string, or stream sentinel")
	}
}

func eventsToAny(events []proto.CanonicalEvent) []any {
	out := make([]any, len(events))
	for i := range events {
		out[i] = events[i]
	}
	return out
}

var _ proto.UpstreamAdapter = (*Adapter)(nil)
