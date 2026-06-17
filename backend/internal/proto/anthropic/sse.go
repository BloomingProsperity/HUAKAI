package anthropic

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"strings"

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
	RequestID           string
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
	// thinking / redacted_thinking 块字段(content_block_start 携带)
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

type anthropicDeltaPayload struct {
	Type        string               `json:"type"`
	Text        string               `json:"text,omitempty"`
	PartialJSON json.RawMessage      `json:"partial_json,omitempty"`
	Signature   string               `json:"signature,omitempty"`
	StopReason  string               `json:"stop_reason,omitempty"`
	Usage       proto.CanonicalUsage `json:"usage,omitempty"`
}

type anthropicBufferedResponse struct {
	ID           string                     `json:"id"`
	Type         string                     `json:"type"`
	Role         string                     `json:"role"`
	Model        string                     `json:"model"`
	Content      []json.RawMessage          `json:"content"`
	StopReason   string                     `json:"stop_reason"`
	StopSequence *string                    `json:"stop_sequence"`
	Usage        *anthropicBufferedUsage    `json:"usage"`
	Passthrough  *proto.PassthroughEnvelope `json:"-"`
}

type anthropicBufferedUsage struct {
	InputTokens              int                              `json:"input_tokens"`
	OutputTokens             int                              `json:"output_tokens"`
	CacheReadInputTokens     int                              `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int                              `json:"cache_creation_input_tokens"`
	CacheCreation            *anthropicCacheCreationBreakdown `json:"cache_creation,omitempty"`
}

type anthropicCacheCreationBreakdown struct {
	Ephemeral5mInputTokens int `json:"ephemeral_5m_input_tokens"`
	Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens"`
}

type anthropicBufferedContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

func (s *Adapter) CanonicalToProviderRequest(ctx context.Context, canonical *proto.HCSF) ([]byte, []proto.ProtocolLossEntry, error) {
	return nil, nil, proto.ErrNotImplemented
}

func (s *Adapter) ProviderResponseToCanonical(ctx context.Context, raw []byte) (*proto.HCSF, []proto.ProtocolLossEntry, error) {
	_ = ctx
	resp, losses, err := anthropicResponseToCanonicalResponse(raw)
	if err != nil {
		return nil, losses, err
	}
	env := proto.NewEmptyEnvelope()
	env.BufferedResponse = &resp
	env.Accounting.Usage = resp.Usage
	if len(losses) > 0 {
		env.CapabilityGraph.ProtocolLoss = append(env.CapabilityGraph.ProtocolLoss, losses...)
	}
	return env, losses, nil
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
		ev := proto.CanonicalEvent{Type: "content_block_start", Index: env.Index, ContentBlock: &block}
		// Stage B: emit a CanonicalUsage with the server tool call count so the
		// UsageAccumulator can accumulate it (+=). ONLY server_tool_use is billable.
		if env.ContentBlock.Type == "server_tool_use" {
			var svcUsage proto.CanonicalUsage
			switch {
			case strings.Contains(env.ContentBlock.Name, "web_search"):
				svcUsage.WebSearchCalls = 1
			case strings.Contains(env.ContentBlock.Name, "file_search") || strings.Contains(env.ContentBlock.Name, "document_search"):
				svcUsage.FileSearchCalls = 1
				// Unknown server tool names: not bucketed — avoid mis-billing.
			}
			if svcUsage.WebSearchCalls > 0 || svcUsage.FileSearchCalls > 0 {
				ev.Usage = &svcUsage
			}
		}
		return []proto.CanonicalEvent{ev}, losses, nil
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
			// 与同适配器 buffered 路径(anthropicBufferedToolUseBlock)对齐：缺失/畸形 id 不丢成空串
			// (空 CallID 会让下游 openai 流硬报错、anthropic 流发出无法关联 tool_result 的 tool_use)，
			// 而是合成一个可用 canonical id 并仅记一条 loss。
			loss := proto.NewLossEntry(proto.FeatureToolUse, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "non-canonical tool call identifier; preserved via synthesized canonical call_id")
			return proto.CanonicalContentBlock{Type: "tool_use", CallID: proto.SynthesizeCanonicalCallID(b.ID), Name: b.Name, Input: b.Input}, []proto.ProtocolLossEntry{loss}
		}
		return proto.CanonicalContentBlock{Type: "tool_use", CallID: callID, Name: b.Name, Input: b.Input}, nil
	case "thinking":
		// 对齐 buffered 路(下方 anthropicBuffered 映射):此前流式把 thinking 折成
		// {Type:"unknown"} = Claude SDK 客户端经中转开 extended thinking 时收到非法
		// 块类型,thinking 输出整条损坏(delta 早已转 reasoning_delta 正常流出)。
		return proto.CanonicalContentBlock{Type: "thinking", Thinking: b.Thinking, Signature: b.Signature}, nil
	case "redacted_thinking":
		return proto.CanonicalContentBlock{Type: "redacted_thinking", Data: append(json.RawMessage(nil), b.Data...)}, nil
	case "server_tool_use":
		// 形状同 tool_use;Stage B 计费在 content_block_start 处按上游原始类型计数
		// (本函数返回值不参与计费判定)。server 端工具 ID 是服务端命名空间
		// (srvtoolu_),不参与 client tool_result 回程映射,保留原始 ID 不走
		// toolu_↔call_ 翻译器(翻译器只认 toolu_ 前缀会误报 malformed)。
		return proto.CanonicalContentBlock{Type: "server_tool_use", CallID: b.ID, Name: b.Name, Input: b.Input}, nil
	default:
		loss := proto.NewLossEntry(proto.FeatureTextStreaming, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "unknown content block type skipped")
		return proto.CanonicalContentBlock{Type: "unknown"}, []proto.ProtocolLossEntry{loss}
	}
}

func anthropicResponseToCanonicalResponse(raw []byte) (proto.CanonicalResponse, []proto.ProtocolLossEntry, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return proto.CanonicalResponse{}, nil, fmt.Errorf("proto: anthropic_messages buffered response empty body")
	}
	var resp anthropicBufferedResponse
	var passthrough proto.PassthroughEnvelope
	if err := proto.UnmarshalWithExtras(raw, &resp, &passthrough); err != nil {
		return proto.CanonicalResponse{}, nil, fmt.Errorf("proto: anthropic_messages buffered response json: %w", err)
	}
	if resp.Type != "message" {
		return proto.CanonicalResponse{}, nil, fmt.Errorf("proto: anthropic_messages buffered response type %q is not message", resp.Type)
	}

	out := proto.CanonicalResponse{
		ID:         resp.ID,
		Model:      resp.Model,
		StopReason: mapStopReason(resp.StopReason),
	}
	if resp.StopSequence != nil {
		out.StopSequence = *resp.StopSequence
	}
	if len(passthrough.Extra) > 0 {
		out.Passthrough = &passthrough
	}

	var losses []proto.ProtocolLossEntry
	losses = append(losses, stopLoss(resp.StopReason)...)
	if resp.Usage == nil {
		losses = append(losses, anthropicResponseLoss(proto.FeatureCacheBreakpoints, "Anthropic buffered response missing usage; billing metadata preserved as zero-value usage"))
	} else {
		out.Usage = resp.Usage.canonical()
	}
	if out.Usage.TotalTokens == 0 {
		out.Usage.TotalTokens = out.Usage.InputTokens + out.Usage.OutputTokens
	}

	if len(resp.Content) == 0 {
		losses = append(losses, anthropicResponseLoss(proto.FeatureTextStreaming, "Anthropic buffered response content array is empty; metadata and usage preserved"))
	}
	for i, rawBlock := range resp.Content {
		block, blockLosses := anthropicBufferedBlockToCanonical(i, rawBlock)
		losses = append(losses, blockLosses...)
		out.Content = append(out.Content, block)
	}
	// Stage B: count server-side built-in tool invocations for per-call surcharge.
	// ONLY type=="server_tool_use" is billable; type=="tool_use" is a free client
	// function-call and MUST NOT be counted (over-charge prevention).
	for _, rawBlock := range resp.Content {
		var blk struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}
		if err2 := json.Unmarshal(rawBlock, &blk); err2 != nil || blk.Type != "server_tool_use" {
			continue
		}
		switch {
		case strings.Contains(blk.Name, "web_search"):
			out.Usage.WebSearchCalls++
		case strings.Contains(blk.Name, "file_search") || strings.Contains(blk.Name, "document_search"):
			out.Usage.FileSearchCalls++
			// Unknown server tool names: intentionally not bucketed to avoid mis-billing.
		}
	}
	return out, losses, nil
}

func (u anthropicBufferedUsage) canonical() proto.CanonicalUsage {
	// Anthropic input_tokens 按官方契约直接复制：它是上游报告的 input token
	// 口径；cache_read/cache_creation 是并列维度，HUAKAI 不在 adapter 内自行扣减
	// cached tokens，避免跨 vendor 二次计算。
	out := proto.CanonicalUsage{
		InputTokens:              u.InputTokens,
		OutputTokens:             u.OutputTokens,
		CacheReadInputTokens:     u.CacheReadInputTokens,
		CacheCreationInputTokens: u.CacheCreationInputTokens,
	}
	if u.CacheCreation != nil {
		out.CacheCreationInputTokens5m = u.CacheCreation.Ephemeral5mInputTokens
		out.CacheCreationInputTokens1h = u.CacheCreation.Ephemeral1hInputTokens
		if out.CacheCreationInputTokens == 0 {
			out.CacheCreationInputTokens = out.CacheCreationInputTokens5m + out.CacheCreationInputTokens1h
		}
	}
	out.TotalTokens = out.InputTokens + out.OutputTokens
	return out
}

func anthropicBufferedBlockToCanonical(index int, raw json.RawMessage) (proto.CanonicalContentBlock, []proto.ProtocolLossEntry) {
	rawCopy := append(json.RawMessage(nil), bytes.TrimSpace(raw)...)
	if len(rawCopy) == 0 || bytes.Equal(rawCopy, []byte("null")) {
		return proto.CanonicalContentBlock{Type: "empty", Raw: rawCopy}, []proto.ProtocolLossEntry{
			anthropicResponseLoss(proto.FeatureTextStreaming, "Anthropic buffered response content block is empty"),
		}
	}
	var block anthropicBufferedContentBlock
	if err := json.Unmarshal(rawCopy, &block); err != nil {
		return proto.CanonicalContentBlock{Type: "unknown", Raw: rawCopy}, []proto.ProtocolLossEntry{
			anthropicResponseLoss(proto.FeatureTextStreaming, "Anthropic buffered response content block JSON shape could not be decoded"),
		}
	}
	switch block.Type {
	case "":
		return proto.CanonicalContentBlock{Type: "empty", Raw: rawCopy}, []proto.ProtocolLossEntry{
			anthropicResponseLoss(proto.FeatureTextStreaming, "Anthropic buffered response content block missing type"),
		}
	case "text":
		out := proto.CanonicalContentBlock{Type: "text", Text: block.Text}
		if anthropicBufferedTextBlockHasExtraFields(rawCopy) {
			out.Raw = rawCopy
			return out, []proto.ProtocolLossEntry{
				anthropicResponseLoss(proto.FeatureTextStreaming, "Anthropic buffered text block has extra fields; preserved original text block as raw canonical content"),
			}
		}
		return out, nil
	case "tool_use":
		return anthropicBufferedToolUseBlock(index, block)
	case "thinking":
		return proto.CanonicalContentBlock{
			Type:      "thinking",
			Thinking:  block.Thinking,
			Signature: block.Signature,
			Raw:       rawCopy,
		}, nil
	case "redacted_thinking":
		return proto.CanonicalContentBlock{
			Type: "redacted_thinking",
			Data: append(json.RawMessage(nil), block.Data...),
			Raw:  rawCopy,
		}, nil
	default:
		return proto.CanonicalContentBlock{Type: block.Type, Raw: rawCopy}, []proto.ProtocolLossEntry{
			anthropicResponseLoss(proto.FeatureTextStreaming, "unknown Anthropic buffered response content block preserved as raw canonical block"),
		}
	}
}

func anthropicBufferedTextBlockHasExtraFields(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return false
	}
	for key := range fields {
		if key != "type" && key != "text" {
			return true
		}
	}
	return false
}

func anthropicBufferedToolUseBlock(index int, block anthropicBufferedContentBlock) (proto.CanonicalContentBlock, []proto.ProtocolLossEntry) {
	var losses []proto.ProtocolLossEntry
	callID := ""
	if block.ID == "" {
		losses = append(losses, anthropicResponseLoss(proto.FeatureToolUse, "Anthropic tool_use block missing id; generated fallback canonical call_id"))
		callID = fallbackAnthropicCallID(index, block)
	} else {
		var err error
		callID, err = proto.ToCanonicalCallID(block.ID, proto.UpstreamProtocolAnthropic)
		if err != nil {
			losses = append(losses, anthropicResponseLoss(proto.FeatureToolUse, "Anthropic tool_use block id malformed; generated fallback canonical call_id"))
			callID = fallbackAnthropicCallID(index, block)
		}
	}
	if block.Name == "" {
		losses = append(losses, anthropicResponseLoss(proto.FeatureToolUse, "Anthropic tool_use block missing name"))
	}
	input, inputLosses := normalizeAnthropicToolInput(block.Input)
	losses = append(losses, inputLosses...)
	return proto.CanonicalContentBlock{
		Type:   "tool_use",
		CallID: callID,
		Name:   block.Name,
		Input:  input,
	}, losses
}

func normalizeAnthropicToolInput(raw json.RawMessage) (json.RawMessage, []proto.ProtocolLossEntry) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || !json.Valid(trimmed) {
		return json.RawMessage("{}"), []proto.ProtocolLossEntry{
			anthropicResponseLoss(proto.FeatureToolUse, "Anthropic tool_use input missing or invalid JSON; normalized to empty object"),
		}
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil || obj == nil {
		return json.RawMessage("{}"), []proto.ProtocolLossEntry{
			anthropicResponseLoss(proto.FeatureToolUse, "Anthropic tool_use input is not a JSON object; normalized to empty object"),
		}
	}
	return append(json.RawMessage(nil), trimmed...), nil
}

func fallbackAnthropicCallID(index int, block anthropicBufferedContentBlock) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%d|%s|%s|%s", index, block.ID, block.Name, string(block.Input))))
	return fmt.Sprintf("call_%x", sum[:8])
}

func anthropicResponseLoss(feature proto.FeatureName, note string) proto.ProtocolLossEntry {
	return proto.NewLossEntry(feature, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, note)
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
		a.CacheCreationInputTokens != 0 || a.CacheReadInputTokens != 0 ||
		a.CacheCreationInputTokens5m != 0 || a.CacheCreationInputTokens1h != 0 {
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
	if b.CacheCreationInputTokens5m != 0 {
		base.CacheCreationInputTokens5m = b.CacheCreationInputTokens5m
	}
	if b.CacheCreationInputTokens1h != 0 {
		base.CacheCreationInputTokens1h = b.CacheCreationInputTokens1h
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
