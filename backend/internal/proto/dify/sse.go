package dify

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

// Adapter 将 Dify 会话 API（SSE / blocking JSON）转换为 proto.HCSF 事件。
//
// Dify SSE 的事件名只在 data JSON 的 "event" 字段里（forwarder 只把 data
// 字节交给 proto adapter），message_end 表示流自然结束，没有 [DONE] 哨兵。
type Adapter struct{}

// SSEEvent 是 SSE 扫描器可传入的最小事件形态。
type SSEEvent struct {
	Type string `json:"type,omitempty"`
	Data []byte `json:"data,omitempty"`
	Done bool   `json:"done,omitempty"`
}

// StreamEnd 是调用方用于表示 Dify SSE 连接自然关闭的哨兵。
type StreamEnd struct{}

// UpstreamState 累积 Dify 流式响应的跨 chunk 状态。
type UpstreamState struct {
	MessageID        string
	MessageStarted   bool
	TextBlockOpen    bool
	TextBlockIndex   int
	NextBlockIndex   int
	Terminated       bool
	AccumulatedUsage proto.CanonicalUsage
	UsageEmitted     bool
	LastStopReason   proto.CanonicalStopReason
	// AccountID / PrefixHash / TenantID: forwarder 注入（与其它 upstream
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
		return nil, nil, errors.New("dify: nil HCSF envelope")
	}
	before := len(canonical.CapabilityGraph.ProtocolLoss)
	body, err := MarshalChatRequest(canonical)
	losses := append([]proto.ProtocolLossEntry(nil), canonical.CapabilityGraph.ProtocolLoss[before:]...)
	return body, losses, err
}

// ProviderResponseToCanonical 解析 blocking 响应。返回带 Version +
// BufferedResponse 的最小 envelope，避免零值 envelope 穿过边界（RequestMeta
// 由 forwarder 层注入，envelope 仅过版本守门）。
func (a *Adapter) ProviderResponseToCanonical(ctx context.Context, raw []byte) (*proto.HCSF, []proto.ProtocolLossEntry, error) {
	_ = ctx
	var resp chatResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, nil, fmt.Errorf("dify: malformed blocking response: %w", err)
	}
	buffered := proto.CanonicalResponse{
		ID:         resp.ConversationID,
		Usage:      usageToCanonical(resp.Metadata.Usage),
		StopReason: proto.CanonicalStopEndTurn,
	}
	if resp.Answer != "" {
		buffered.Content = []proto.CanonicalContentBlock{{Type: "text", Text: resp.Answer}}
	}
	env := &proto.HCSF{
		Version:          proto.HCSFVersion,
		BufferedResponse: &buffered,
	}
	return env, nil, nil
}

// ProviderEventToCanonicalEvents 按 data JSON 的 event 字段分发流式帧。
func (a *Adapter) ProviderEventToCanonicalEvents(ctx context.Context, providerEvt any, state any) ([]any, []proto.ProtocolLossEntry, error) {
	_ = ctx
	st, ok := state.(*UpstreamState)
	if !ok {
		return nil, nil, fmt.Errorf("proto: expected *dify.UpstreamState")
	}
	data, streamEnd, err := coerceSSEData(providerEvt)
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
	// Dify 无 [DONE] 哨兵；容忍代理层补发的哨兵，按流结束处理。
	if payload == "[DONE]" {
		return eventsToAny(finalizeState(st)), nil, nil
	}

	var chunk streamChunk
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		loss := proto.NewLossEntry(proto.FeatureTextStreaming, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "malformed Dify SSE JSON chunk skipped")
		return nil, []proto.ProtocolLossEntry{loss}, nil
	}
	return chunkToCanonicalEvents(chunk, st)
}

// FinalizeUpstreamStream 在上游 EOF 无 message_end 时补齐终止事件。
// message_end 已置 Terminated 时本函数零输出（双触发去重）。
func (a *Adapter) FinalizeUpstreamStream(ctx context.Context, state any) ([]any, error) {
	_ = ctx
	st, ok := state.(*UpstreamState)
	if !ok {
		return nil, fmt.Errorf("proto: expected *dify.UpstreamState")
	}
	return eventsToAny(finalizeState(st)), nil
}

func chunkToCanonicalEvents(chunk streamChunk, st *UpstreamState) ([]any, []proto.ProtocolLossEntry, error) {
	// post-terminal 守卫:message_end 之后的任何事件不得再进 canonical 流
	// (message_stop 已发,继续发 delta 会破坏客户端事件序);丢弃并记账。
	if st.Terminated {
		loss := proto.NewLossEntry(proto.FeatureTextStreaming, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "dify stream event after message_end skipped")
		return nil, []proto.ProtocolLossEntry{loss}, nil
	}
	// ping 是保活心跳,无任何载荷;静默跳过,不记 loss(避免长流账面噪音)。
	if chunk.Event == "ping" {
		return nil, nil, nil
	}
	if chunk.ConversationID != "" {
		st.MessageID = chunk.ConversationID
	}
	switch chunk.Event {
	case "message", "agent_message":
		// agent_message 是 agent 编排下的同形增量事件，与 message 同路处理。
		if chunk.Answer == "" {
			return nil, nil, nil
		}
		var events []proto.CanonicalEvent
		events = append(events, ensureMessageStart(st)...)
		events = append(events, ensureTextBlock(st)...)
		events = append(events, proto.CanonicalEvent{
			Type:  "content_block_delta",
			Index: st.TextBlockIndex,
			Delta: &proto.CanonicalContentDelta{Type: "text_delta", Text: chunk.Answer},
		})
		return eventsToAny(events), nil, nil
	case "message_end":
		if chunk.Metadata != nil && chunk.Metadata.Usage != nil {
			st.AccumulatedUsage = usageToCanonical(chunk.Metadata.Usage)
		}
		st.LastStopReason = proto.CanonicalStopEndTurn
		var events []proto.CanonicalEvent
		events = append(events, ensureMessageStart(st)...)
		events = appendOpenBlockStop(events, st)
		var usage *proto.CanonicalUsage
		if proto.UsageHasValue(st.AccumulatedUsage) {
			value := st.AccumulatedUsage
			usage = &value
			st.UsageEmitted = true
		}
		events = append(events,
			proto.CanonicalEvent{Type: "message_delta", Usage: usage, StopReason: st.LastStopReason},
			proto.CanonicalEvent{Type: "message_stop"},
		)
		st.Terminated = true
		return eventsToAny(events), nil, nil
	case "error":
		// fail loud：上游显式 error 事件不可静默吞成空流。
		return nil, nil, fmt.Errorf("dify: upstream stream error event (status=%d code=%q): %s", chunk.Status, chunk.Code, chunk.Message)
	default:
		// workflow_started / node_started / node_finished 等编排事件以及未知
		// 事件不含用户文本：丢弃但记账，禁止静默蒸发。
		loss := proto.NewLossEntry(proto.FeatureTextStreaming, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, fmt.Sprintf("dify stream event %q carries no user-visible text; skipped", chunk.Event))
		return nil, []proto.ProtocolLossEntry{loss}, nil
	}
}

func ensureMessageStart(st *UpstreamState) []proto.CanonicalEvent {
	if st.MessageStarted {
		return nil
	}
	st.MessageStarted = true
	return []proto.CanonicalEvent{{
		Type:      "message_start",
		MessageID: st.MessageID,
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

// finalizeState 补齐未终止流的收尾事件；Terminated 标志保证与 message_end
// 双触发时只发一次。
func finalizeState(st *UpstreamState) []proto.CanonicalEvent {
	if st.Terminated {
		return nil
	}
	var events []proto.CanonicalEvent
	events = appendOpenBlockStop(events, st)
	if proto.UsageHasValue(st.AccumulatedUsage) && !st.UsageEmitted {
		usage := st.AccumulatedUsage
		events = append(events, proto.CanonicalEvent{Type: "message_delta", Usage: &usage, StopReason: st.LastStopReason})
		st.UsageEmitted = true
	}
	st.Terminated = true
	events = append(events, proto.CanonicalEvent{Type: "message_stop"})
	return events
}

func usageToCanonical(u *usagePayload) proto.CanonicalUsage {
	if u == nil {
		return proto.CanonicalUsage{}
	}
	out := proto.CanonicalUsage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
		TotalTokens:  u.TotalTokens,
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.InputTokens + out.OutputTokens
	}
	return out
}

// coerceSSEData 容忍多种调用方事件形态：[]byte / string / json.RawMessage /
// SSEEvent / 流结束哨兵 / io.EOF。
func coerceSSEData(v any) ([]byte, bool, error) {
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
		return extractSSEData(evt.Data), false, nil
	case json.RawMessage:
		return extractSSEData(evt), false, nil
	case []byte:
		return extractSSEData(evt), false, nil
	case string:
		return extractSSEData([]byte(evt)), false, nil
	case error:
		if errors.Is(evt, io.EOF) {
			return nil, true, nil
		}
		return nil, false, evt
	default:
		return nil, false, fmt.Errorf("proto: expected SSEEvent, []byte, string, or stream sentinel")
	}
}

// extractSSEData 从可能携带 "event:"/"data:" 行的原始帧里取 data 载荷。
// 注意：事件分发只认 data JSON 内的 "event" 字段，不认 SSE event: 行——
// forwarder 正常只交付 data 字节，event: 行只在 raw 帧直灌时出现。
func extractSSEData(raw []byte) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	lines := bytes.Split(trimmed, []byte{'\n'})
	var data bytes.Buffer
	found := false
	for _, line := range lines {
		line = bytes.TrimRight(line, "\r")
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		part := bytes.TrimPrefix(line, []byte("data:"))
		if len(part) > 0 && part[0] == ' ' {
			part = part[1:]
		}
		if data.Len() > 0 {
			data.WriteByte('\n')
		}
		data.Write(part)
		found = true
	}
	if found {
		return bytes.TrimSpace(data.Bytes())
	}
	return trimmed
}

func eventsToAny(events []proto.CanonicalEvent) []any {
	out := make([]any, len(events))
	for i := range events {
		out[i] = events[i]
	}
	return out
}

var _ proto.UpstreamAdapter = (*Adapter)(nil)
