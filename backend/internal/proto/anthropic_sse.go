package proto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/BloomingProsperity/HUAKAI/internal/cachemetrics"
)

var ErrUnknownEventType = errors.New("proto: unknown upstream event type")

var ErrNotImplemented = errors.New("proto: not implemented")

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
	AccumulatedUsage    CanonicalUsage
	DeliveredChunkCount int64
	AccountID           int64
	PrefixHash          string
	// M5b: TenantID 透传 — ObserveByAccountWithPrefix 必填; 0 时 observer
	// 仍记 expvar counter 但跳过段表更新 (无 tenant 信息)。 forwarder.go
	// newUpstreamState 从 ForwardRequest.TenantID 注入。
	TenantID int64
}

// AnthropicAdapter translates Anthropic SSE events through HCSF per spec section 3 Phase C.
type AnthropicAdapter struct {
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
	Usage        CanonicalUsage          `json:"usage,omitempty"`
}

type anthropicMessagePayload struct {
	ID    string         `json:"id,omitempty"`
	Model string         `json:"model,omitempty"`
	Usage CanonicalUsage `json:"usage,omitempty"`
}

type anthropicBlockPayload struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type anthropicDeltaPayload struct {
	Type        string          `json:"type"`
	Text        string          `json:"text,omitempty"`
	PartialJSON json.RawMessage `json:"partial_json,omitempty"`
	Signature   string          `json:"signature,omitempty"`
	StopReason  string          `json:"stop_reason,omitempty"`
	Usage       CanonicalUsage  `json:"usage,omitempty"`
}

func (s *AnthropicAdapter) CanonicalToProviderRequest(ctx context.Context, canonical *HCSF) ([]byte, []ProtocolLossEntry, error) {
	return nil, nil, ErrNotImplemented
}

func (s *AnthropicAdapter) ProviderResponseToCanonical(ctx context.Context, raw []byte) (*HCSF, []ProtocolLossEntry, error) {
	return nil, nil, ErrNotImplemented
}

func (s *AnthropicAdapter) ProviderEventToCanonicalEvents(ctx context.Context, providerEvt any, state any) ([]any, []ProtocolLossEntry, error) {
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

func (s *AnthropicAdapter) providerEventToCanonicalEvents(evt anthropicEvent, state *UpstreamState) ([]CanonicalEvent, []ProtocolLossEntry, error) {
	if state.BlocksInProgress == nil {
		state.BlocksInProgress = map[int]bool{}
	}
	var env anthropicEnvelope
	// U7-D：用 UnmarshalWithExtras 同时拿 known + 上游 unknown（envelope 级
	// 字段：vendor 在 message_start / message_delta 等顶层加新字段时透传）。
	// 内层 message.usage / delta / content_block 的 unknown 暂不抓——若需扩，
	// 在对应嵌套 typed struct 各加一层 envelope（U7 后续）。
	var passthrough PassthroughEnvelope
	if len(evt.Raw) > 0 {
		if err := UnmarshalWithExtras(evt.Raw, &env, &passthrough); err != nil {
			return nil, nil, err
		}
	}
	events, losses, switchErr := s.providerEventSwitch(evt, env, state)
	if switchErr != nil {
		return events, losses, switchErr
	}
	if len(passthrough.Extra) > 0 && len(events) > 0 {
		events[0].Passthrough = &passthrough
	}
	return events, losses, nil
}

// providerEventSwitch 是原 switch 主体，提取出来便于上层在 unmarshal 后
// 附加 Passthrough。逻辑与之前等价——纯重构。
func (s *AnthropicAdapter) providerEventSwitch(evt anthropicEvent, env anthropicEnvelope, state *UpstreamState) ([]CanonicalEvent, []ProtocolLossEntry, error) {
	switch evt.Type {
	case "message_start":
		state.MessageID = env.Message.ID
		state.AccumulatedUsage = env.Message.Usage
		return []CanonicalEvent{{Type: "message_start", MessageID: env.Message.ID, Model: env.Message.Model, Usage: &env.Message.Usage}}, nil, nil
	case "content_block_start":
		state.CurrentBlockIndex = env.Index
		state.BlocksInProgress[env.Index] = true
		block, losses := canonicalBlock(env.ContentBlock)
		return []CanonicalEvent{{Type: "content_block_start", Index: env.Index, ContentBlock: &block}}, losses, nil
	case "content_block_delta":
		if anthropicDeltaDelivered(env.Delta) {
			state.DeliveredChunkCount++
		}
		delta, losses := s.canonicalDelta(env.Delta)
		if delta == nil {
			return nil, losses, nil
		}
		return []CanonicalEvent{{Type: "content_block_delta", Index: env.Index, Delta: delta}}, losses, nil
	case "content_block_stop":
		delete(state.BlocksInProgress, env.Index)
		return []CanonicalEvent{{Type: "content_block_stop", Index: env.Index}}, nil, nil
	case "message_delta":
		state.AccumulatedUsage = mergeUsage(state.AccumulatedUsage, env.Usage, env.Delta.Usage)
		stop := mapStopReason(env.Delta.StopReason)
		return []CanonicalEvent{{Type: "message_delta", Usage: &state.AccumulatedUsage, StopReason: stop}}, stopLoss(env.Delta.StopReason), nil
	case "message_stop":
		if state.Terminated {
			loss := newLossEntry(FeatureTextStreaming, DirectionUpstreamToCanonical, VerdictLossy, "duplicate terminal event skipped")
			return nil, []ProtocolLossEntry{loss}, nil
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
		return []CanonicalEvent{{Type: "message_stop"}}, nil, nil
	default:
		loss := newLossEntry(FeatureTextStreaming, DirectionUpstreamToCanonical, VerdictLossy, "unknown upstream event type skipped")
		return nil, []ProtocolLossEntry{loss}, fmt.Errorf("%w: %s", ErrUnknownEventType, evt.Type)
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

func (s *AnthropicAdapter) FinalizeUpstreamStream(ctx context.Context, state any) ([]any, error) {
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
		out = append(out, CanonicalEvent{Type: "content_block_stop", Index: idx})
	}
	st.Terminated = true
	out = append(out, CanonicalEvent{Type: "message_stop"})
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

func canonicalBlock(b anthropicBlockPayload) (CanonicalContentBlock, []ProtocolLossEntry) {
	switch b.Type {
	case "text":
		return CanonicalContentBlock{Type: "text", Text: b.Text}, nil
	case "tool_use":
		callID, err := ToCanonicalCallID(b.ID, UpstreamProtocolAnthropic)
		if err != nil {
			loss := newLossEntry(FeatureToolUse, DirectionUpstreamToCanonical, VerdictLossy, "malformed tool call identifier")
			return CanonicalContentBlock{Type: "tool_use", Name: b.Name, Input: b.Input}, []ProtocolLossEntry{loss}
		}
		return CanonicalContentBlock{Type: "tool_use", CallID: callID, Name: b.Name, Input: b.Input}, nil
	default:
		loss := newLossEntry(FeatureTextStreaming, DirectionUpstreamToCanonical, VerdictLossy, "unknown content block type skipped")
		return CanonicalContentBlock{Type: "unknown"}, []ProtocolLossEntry{loss}
	}
}

func (s *AnthropicAdapter) canonicalDelta(d anthropicDeltaPayload) (*CanonicalContentDelta, []ProtocolLossEntry) {
	switch d.Type {
	case "text_delta":
		return &CanonicalContentDelta{Type: "text_delta", Text: d.Text}, nil
	case "input_json_delta":
		return &CanonicalContentDelta{Type: "tool_input_delta", PartialJSON: d.PartialJSON}, nil
	case "thinking_delta":
		return &CanonicalContentDelta{Type: "reasoning_delta", ReasoningText: d.Text}, nil
	case "signature_delta":
		loss := newLossEntry(FeatureSignatureDelta, DirectionUpstreamToCanonical, VerdictLossy, "signature delta skipped by policy")
		if !s.CarryForwardSignatureDelta {
			return nil, []ProtocolLossEntry{loss}
		}
		return &CanonicalContentDelta{Type: "signature_delta", Signature: d.Signature}, nil
	default:
		loss := newLossEntry(FeatureTextStreaming, DirectionUpstreamToCanonical, VerdictLossy, "unknown content delta type skipped")
		return nil, []ProtocolLossEntry{loss}
	}
}

func mergeUsage(base, a, b CanonicalUsage) CanonicalUsage {
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

func mapStopReason(reason string) CanonicalStopReason {
	switch reason {
	case "", "end_turn":
		return CanonicalStopEndTurn
	case "max_tokens":
		return CanonicalStopMaxTokens
	case "stop_sequence":
		return CanonicalStopSequence
	case "tool_use":
		return CanonicalStopToolUse
	case "refusal":
		return CanonicalStopRefusal
	default:
		return CanonicalStopUnknown
	}
}

func stopLoss(reason string) []ProtocolLossEntry {
	if reason == "" || mapStopReason(reason) != CanonicalStopUnknown {
		return nil
	}
	return []ProtocolLossEntry{newLossEntry(FeatureMaxTokensFinishReason, DirectionUpstreamToCanonical, VerdictLossy, "unknown stop reason mapped to canonical unknown")}
}

var _ UpstreamAdapter = (*AnthropicAdapter)(nil)
