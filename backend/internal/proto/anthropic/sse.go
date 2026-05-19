package anthropic

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/BloomingProsperity/HUAKAI/internal/cachemetrics"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// UpstreamState tracks Anthropic SSE translation state from spec section 3 Phase C.
//
// AccountID（Track P）: forwarder 注入选定的 provider_account_id, 让 adapter
// 在终态调 cachemetrics.ObserveByAccount 累计 per-account 维度。零值表示
// 不分账号 (退化全局观测)。
//
// PrefixHash（PASR-lite A4）: forwarder 注入 ForwardRequest.SessionHash
// (上游已 hash 的 prompt prefix), 让 adapter 在终态调
// cachemetrics.ObserveByAccountWithPrefix, 把 (acc, prefix, creation, read)
// 推给 PASR observer 更新 PrefixSegment.HasCacheBitmap / LastReadAt。
// 空串表示无 prefix 信息 (退化为只更新 per-account counter, 不触发 PASR)。
type UpstreamState struct {
	MessageID           string
	CurrentBlockIndex   int
	BlocksInProgress    map[int]bool
	Terminated          bool
	AccumulatedUsage    proto.CanonicalUsage
	DeliveredChunkCount int64
	AccountID           int64
	PrefixHash          string
	// M5b: TenantID 透传 — ObserveByAccountWithPrefix 必填; 0 时 observer
	// 仍记 expvar counter 但跳过段表更新 (无 tenant 信息)。 forwarder.go
	// newUpstreamState 从 ForwardRequest.TenantID 注入。
	TenantID int64
}

// Adapter translates Anthropic SSE events through proto.HCSF per spec section 3 Phase C.
type Adapter struct {
	CarryForwardSignatureDelta bool
}

type anthropicEvent struct {
	Type string          `json:"type"`
	Raw  json.RawMessage `json:"raw,omitempty"`
}

type anthropicEnvelope struct {
	Type         string                  `json:"type"`
	Message      anthropicMessagePayload `json:"message,omitempty"`
	Index        int                     `json:"index,omitempty"`
	ContentBlock anthropicBlockPayload   `json:"content_block,omitempty"`
	Delta        anthropicDeltaPayload   `json:"delta,omitempty"`
	Usage        proto.CanonicalUsage    `json:"usage,omitempty"`
}

type anthropicMessagePayload struct {
	ID    string               `json:"id,omitempty"`
	Model string               `json:"model,omitempty"`
	Usage proto.CanonicalUsage `json:"usage,omitempty"`
}

type anthropicBlockPayload struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type anthropicDeltaPayload struct {
	Type        string               `json:"type"`
	Text        string               `json:"text,omitempty"`
	PartialJSON json.RawMessage      `json:"partial_json,omitempty"`
	Signature   string               `json:"signature,omitempty"`
	StopReason  string               `json:"stop_reason,omitempty"`
	Usage       proto.CanonicalUsage `json:"usage,omitempty"`
}

func (s *Adapter) CanonicalToProviderRequest(ctx context.Context, canonical *proto.HCSF) ([]byte, []proto.ProtocolLossEntry, error) {
	return nil, nil, proto.ErrNotImplemented
}

func (s *Adapter) ProviderResponseToCanonical(ctx context.Context, raw []byte) (*proto.HCSF, []proto.ProtocolLossEntry, error) {
	return nil, nil, proto.ErrNotImplemented
}

func (s *Adapter) ProviderEventToCanonicalEvents(ctx context.Context, providerEvt any, state any) ([]any, []proto.ProtocolLossEntry, error) {
	_ = ctx
	st, ok := state.(*UpstreamState)
	if !ok {
		return nil, nil, fmt.Errorf("proto: expected *UpstreamState")
	}
	evt, err := coerceAnthropicEvent(providerEvt)
	if err != nil {
		return nil, nil, err
	}
	events, losses, err := s.providerEventToCanonicalEvents(evt, st)
	out := make([]any, len(events))
	for i := range events {
		out[i] = events[i]
	}
	return out, losses, err
}

func (s *Adapter) providerEventToCanonicalEvents(evt anthropicEvent, state *UpstreamState) ([]proto.CanonicalEvent, []proto.ProtocolLossEntry, error) {
	if state.BlocksInProgress == nil {
		state.BlocksInProgress = map[int]bool{}
	}
	var env anthropicEnvelope
	// U7-D：用 proto.UnmarshalWithExtras 同时拿 known + 上游 unknown（envelope 级
	// 字段：vendor 在 message_start / message_delta 等顶层加新字段时透传）。
	// 内层 message.usage / delta / content_block 的 unknown 暂不抓——若需扩，
	// 在对应嵌套 typed struct 各加一层 envelope（U7 后续）。
	var passthrough proto.PassthroughEnvelope
	if len(evt.Raw) > 0 {
		if err := proto.UnmarshalWithExtras(evt.Raw, &env, &passthrough); err != nil {
			return nil, nil, err
		}
	}
	events, losses, switchErr := s.providerEventSwitch(evt, env, state)
	if switchErr != nil {
		return events, losses, switchErr
	}
	events = attachPassthroughToFirstEvent(events, passthrough)
	return events, losses, nil
}

// providerEventSwitch 是原 switch 主体，提取出来便于上层在 unmarshal 后
// 附加 Passthrough。逻辑与之前等价——纯重构。
func (s *Adapter) providerEventSwitch(evt anthropicEvent, env anthropicEnvelope, state *UpstreamState) ([]proto.CanonicalEvent, []proto.ProtocolLossEntry, error) {
	switch evt.Type {
	case "message_start":
		state.MessageID = env.Message.ID
		state.AccumulatedUsage = env.Message.Usage
		return []proto.CanonicalEvent{{Type: "message_start", MessageID: env.Message.ID, Model: env.Message.Model, Usage: &env.Message.Usage}}, nil, nil
	case "content_block_start":
		state.CurrentBlockIndex = env.Index
		state.BlocksInProgress[env.Index] = true
		block, losses := canonicalBlock(env.ContentBlock)
		return []proto.CanonicalEvent{{Type: "content_block_start", Index: env.Index, ContentBlock: &block}}, losses, nil
	case "content_block_delta":
		if anthropicDeltaDelivered(env.Delta) {
			state.DeliveredChunkCount++
		}
		delta, losses := s.canonicalDelta(env.Delta)
		if delta == nil {
			return nil, losses, nil
		}
		return []proto.CanonicalEvent{{Type: "content_block_delta", Index: env.Index, Delta: delta}}, losses, nil
	case "content_block_stop":
		delete(state.BlocksInProgress, env.Index)
		return []proto.CanonicalEvent{{Type: "content_block_stop", Index: env.Index}}, nil, nil
	case "message_delta":
		state.AccumulatedUsage = mergeUsage(state.AccumulatedUsage, env.Usage, env.Delta.Usage)
		stop := mapStopReason(env.Delta.StopReason)
		return []proto.CanonicalEvent{{Type: "message_delta", Usage: &state.AccumulatedUsage, StopReason: stop}}, stopLoss(env.Delta.StopReason), nil
	case "message_stop":
		if state.Terminated {
			loss := proto.NewLossEntry(proto.FeatureTextStreaming, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "duplicate terminal event skipped")
			return nil, []proto.ProtocolLossEntry{loss}, nil
		}
		state.Terminated = true
		// 观测 vendor cache token 命中率（Track D + PASR-lite A4）。
		// 0/0 不增 counter (Observe 内置 short-circuit 防 inflate 分母).
		// WithPrefix 比 ObserveByAccount 多触发 PASR-lite observer, 把
		// (acc, prefix, creation, read) 推给 PASRSelector 更新 segment 状态。
		cachemetrics.ObserveByAccountWithPrefix(
			int64(state.AccumulatedUsage.CacheCreationInputTokens),
			int64(state.AccumulatedUsage.CacheReadInputTokens),
			state.TenantID,
			state.AccountID,
			state.PrefixHash,
		)
		return []proto.CanonicalEvent{{Type: "message_stop"}}, nil, nil
	default:
		loss := proto.NewLossEntry(proto.FeatureTextStreaming, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "unknown upstream event type skipped")
		return nil, []proto.ProtocolLossEntry{loss}, fmt.Errorf("%w: %s", proto.ErrUnknownEventType, evt.Type)
	}
}

func anthropicDeltaDelivered(delta anthropicDeltaPayload) bool {
	switch delta.Type {
	case "text_delta", "input_json_delta", "thinking_delta":
		return delta.Text != "" || len(delta.PartialJSON) > 0
	default:
		return false
	}
}

func (s *Adapter) FinalizeUpstreamStream(ctx context.Context, state any) ([]any, error) {
	_ = ctx
	st, ok := state.(*UpstreamState)
	if !ok {
		return nil, fmt.Errorf("proto: expected *UpstreamState")
	}
	if st.Terminated {
		return nil, nil
	}
	// 合成 terminal 路径也观测 cache 命中（与 message_stop case 同等语义）。
	// PASR-lite A4: 用 WithPrefix 变体让 PASR observer 也收到反馈。
	cachemetrics.ObserveByAccountWithPrefix(
		int64(st.AccumulatedUsage.CacheCreationInputTokens),
		int64(st.AccumulatedUsage.CacheReadInputTokens),
		st.TenantID,
		st.AccountID,
		st.PrefixHash,
	)
	var out []any
	for idx := range st.BlocksInProgress {
		out = append(out, proto.CanonicalEvent{Type: "content_block_stop", Index: idx})
	}
	st.Terminated = true
	out = append(out, proto.CanonicalEvent{Type: "message_stop"})
	return out, nil
}

func coerceAnthropicEvent(v any) (anthropicEvent, error) {
	switch evt := v.(type) {
	case anthropicEvent:
		return evt, nil
	case []byte:
		var env anthropicEnvelope
		if err := json.Unmarshal(evt, &env); err != nil {
			return anthropicEvent{}, err
		}
		return anthropicEvent{Type: env.Type, Raw: evt}, nil
	default:
		return anthropicEvent{}, fmt.Errorf("proto: expected anthropicEvent")
	}
}

func canonicalBlock(b anthropicBlockPayload) (proto.CanonicalContentBlock, []proto.ProtocolLossEntry) {
	switch b.Type {
	case "text":
		return proto.CanonicalContentBlock{Type: "text", Text: b.Text}, nil
	case "tool_use":
		callID, err := proto.ToCanonicalCallID(b.ID, proto.UpstreamProtocolAnthropic)
		if err != nil {
			loss := proto.NewLossEntry(proto.FeatureToolUse, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "malformed tool call identifier")
			return proto.CanonicalContentBlock{Type: "tool_use", Name: b.Name, Input: b.Input}, []proto.ProtocolLossEntry{loss}
		}
		return proto.CanonicalContentBlock{Type: "tool_use", CallID: callID, Name: b.Name, Input: b.Input}, nil
	default:
		loss := proto.NewLossEntry(proto.FeatureTextStreaming, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "unknown content block type skipped")
		return proto.CanonicalContentBlock{Type: "unknown"}, []proto.ProtocolLossEntry{loss}
	}
}

func (s *Adapter) canonicalDelta(d anthropicDeltaPayload) (*proto.CanonicalContentDelta, []proto.ProtocolLossEntry) {
	switch d.Type {
	case "text_delta":
		return &proto.CanonicalContentDelta{Type: "text_delta", Text: d.Text}, nil
	case "input_json_delta":
		return &proto.CanonicalContentDelta{Type: "tool_input_delta", PartialJSON: d.PartialJSON}, nil
	case "thinking_delta":
		return &proto.CanonicalContentDelta{Type: "reasoning_delta", ReasoningText: d.Text}, nil
	case "signature_delta":
		loss := proto.NewLossEntry(proto.FeatureSignatureDelta, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "signature delta skipped by policy")
		if !s.CarryForwardSignatureDelta {
			return nil, []proto.ProtocolLossEntry{loss}
		}
		return &proto.CanonicalContentDelta{Type: "signature_delta", Signature: d.Signature}, nil
	default:
		loss := proto.NewLossEntry(proto.FeatureTextStreaming, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "unknown content delta type skipped")
		return nil, []proto.ProtocolLossEntry{loss}
	}
}

func mergeUsage(base, a, b proto.CanonicalUsage) proto.CanonicalUsage {
	if a.InputTokens != 0 || a.OutputTokens != 0 || a.TotalTokens != 0 ||
		a.CacheCreationInputTokens != 0 || a.CacheReadInputTokens != 0 {
		base = a
	}
	if b.InputTokens != 0 {
		base.InputTokens = b.InputTokens
	}
	if b.OutputTokens != 0 {
		base.OutputTokens = b.OutputTokens
	}
	if b.TotalTokens != 0 {
		base.TotalTokens = b.TotalTokens
	}
	if b.CacheCreationInputTokens != 0 {
		base.CacheCreationInputTokens = b.CacheCreationInputTokens
	}
	if b.CacheReadInputTokens != 0 {
		base.CacheReadInputTokens = b.CacheReadInputTokens
	}
	if base.TotalTokens == 0 {
		base.TotalTokens = base.InputTokens + base.OutputTokens
	}
	return base
}

func mapStopReason(reason string) proto.CanonicalStopReason {
	switch reason {
	case "", "end_turn":
		return proto.CanonicalStopEndTurn
	case "max_tokens":
		return proto.CanonicalStopMaxTokens
	case "stop_sequence":
		return proto.CanonicalStopSequence
	case "tool_use":
		return proto.CanonicalStopToolUse
	case "refusal":
		return proto.CanonicalStopRefusal
	default:
		return proto.CanonicalStopUnknown
	}
}

func stopLoss(reason string) []proto.ProtocolLossEntry {
	if reason == "" || mapStopReason(reason) != proto.CanonicalStopUnknown {
		return nil
	}
	return []proto.ProtocolLossEntry{proto.NewLossEntry(proto.FeatureMaxTokensFinishReason, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "unknown stop reason mapped to canonical unknown")}
}

var _ proto.UpstreamAdapter = (*Adapter)(nil)
