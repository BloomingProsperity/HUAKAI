package proto

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/cachemetrics"
)

// OpenAIAdapter 将 OpenAI Chat Completions SSE 转换为 HCSF 事件。
type OpenAIAdapter struct{}

// OpenAISSEEvent 是上游 SSE 扫描器可传入的最小事件形状。
type OpenAISSEEvent struct {
	Type string `json:"type,omitempty"`
	Data []byte `json:"data"`
}

// OpenAIUpstreamState 累计 OpenAI 流式 chunk 的跨事件状态。
type OpenAIUpstreamState struct {
	MessageID          string
	Model              string
	MessageStarted     bool
	Terminated         bool
	TextBlockStarted   bool
	TextBlockOpen      bool
	TextBlockIndex     int
	NextBlockIndex     int
	AccumulatedContent string
	AccumulatedUsage   CanonicalUsage
	UsageEmitted       bool
	LastStopReason     CanonicalStopReason
	RawFinishReason    string
	ToolCalls          map[int]*OpenAIToolCallState
	// AccountID（Track P）: forwarder 注入. cachemetrics.ObserveByAccount 用。
	AccountID int64
}

// OpenAIToolCallState 累计同一个 tool_call.index 的增量内容。
type OpenAIToolCallState struct {
	Index       int
	BlockIndex  int
	ID          string
	CanonicalID string
	Type        string
	Name        string
	Arguments   string
	Started     bool
	Open        bool
}

type openAIChatCompletionChunk struct {
	ID      string               `json:"id,omitempty"`
	Object  string               `json:"object,omitempty"`
	Model   string               `json:"model,omitempty"`
	Choices []openAIStreamChoice `json:"choices,omitempty"`
	Usage   *openAIUsage         `json:"usage,omitempty"`
}

type openAIStreamChoice struct {
	Index        int               `json:"index,omitempty"`
	Delta        openAIStreamDelta `json:"delta,omitempty"`
	FinishReason *string           `json:"finish_reason,omitempty"`
}

type openAIStreamDelta struct {
	Role      string                 `json:"role,omitempty"`
	Content   *string                `json:"content,omitempty"`
	ToolCalls []openAIStreamToolCall `json:"tool_calls,omitempty"`
	Refusal   *string                `json:"refusal,omitempty"`
}

type openAIStreamToolCall struct {
	Index    int                  `json:"index,omitempty"`
	ID       string               `json:"id,omitempty"`
	Type     string               `json:"type,omitempty"`
	Function openAIStreamFunction `json:"function,omitempty"`
}

type openAIStreamFunction struct {
	Name      *string `json:"name,omitempty"`
	Arguments *string `json:"arguments,omitempty"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
	// OpenAI prompt caching: usage.prompt_tokens_details.cached_tokens
	// 表示该请求命中缓存的 prompt token 数。OpenAI 没有"创建缓存"的概念
	// （implicit caching），只暴露读命中。映射到 CanonicalUsage.CacheReadInputTokens。
	// (sonnet F4 MEDIUM 修复: 缺失 OpenAI cache 观测)
	PromptTokensDetails *openAIPromptTokensDetails `json:"prompt_tokens_details,omitempty"`
}

type openAIPromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
	AudioTokens  int `json:"audio_tokens,omitempty"`
}

type openAIChatCompletionResponse struct {
	ID      string                 `json:"id,omitempty"`
	Object  string                 `json:"object,omitempty"`
	Model   string                 `json:"model,omitempty"`
	Choices []openAIResponseChoice `json:"choices,omitempty"`
	Usage   *openAIUsage           `json:"usage,omitempty"`
}

type openAIResponseChoice struct {
	Index        int                   `json:"index,omitempty"`
	Message      openAIResponseMessage `json:"message,omitempty"`
	FinishReason string                `json:"finish_reason,omitempty"`
}

type openAIResponseMessage struct {
	Role      string                   `json:"role,omitempty"`
	Content   json.RawMessage          `json:"content,omitempty"`
	ToolCalls []openAIResponseToolCall `json:"tool_calls,omitempty"`
}

type openAIResponseToolCall struct {
	ID       string                 `json:"id,omitempty"`
	Type     string                 `json:"type,omitempty"`
	Function openAIResponseFunction `json:"function,omitempty"`
}

type openAIResponseFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

func (a *OpenAIAdapter) CanonicalToProviderRequest(ctx context.Context, canonical *HCSF) ([]byte, []ProtocolLossEntry, error) {
	_ = ctx
	_ = canonical
	return nil, nil, ErrNotImplemented
}

func (a *OpenAIAdapter) ProviderResponseToCanonical(ctx context.Context, raw []byte) (*HCSF, []ProtocolLossEntry, error) {
	_ = ctx
	_, losses, err := openAIResponseToCanonicalResponse(raw)
	if err != nil {
		return nil, losses, err
	}
	losses = append(losses, newLossEntry(FeatureTextStreaming, DirectionUpstreamToCanonical, VerdictLossy, "HCSF envelope has no buffered response slot in this slice"))
	return &HCSF{}, losses, nil
}

func (a *OpenAIAdapter) ProviderEventToCanonicalEvents(ctx context.Context, providerEvt any, state any) ([]any, []ProtocolLossEntry, error) {
	_ = ctx
	st, ok := state.(*OpenAIUpstreamState)
	if !ok {
		return nil, nil, fmt.Errorf("proto: expected *OpenAIUpstreamState")
	}
	data, err := coerceOpenAISSEData(providerEvt)
	if err != nil {
		return nil, nil, err
	}
	events, losses := a.providerDataToCanonicalEvents(data, st)
	out := make([]any, len(events))
	for i := range events {
		out[i] = events[i]
	}
	return out, losses, nil
}

func (a *OpenAIAdapter) FinalizeUpstreamStream(ctx context.Context, state any) ([]any, error) {
	_ = ctx
	st, ok := state.(*OpenAIUpstreamState)
	if !ok {
		return nil, fmt.Errorf("proto: expected *OpenAIUpstreamState")
	}
	events, _ := finalizeOpenAIState(st, false)
	out := make([]any, len(events))
	for i := range events {
		out[i] = events[i]
	}
	return out, nil
}

func (a *OpenAIAdapter) providerDataToCanonicalEvents(data []byte, state *OpenAIUpstreamState) ([]CanonicalEvent, []ProtocolLossEntry) {
	payload := strings.TrimSpace(string(data))
	if payload == "" {
		return nil, nil
	}
	if payload == "[DONE]" {
		return finalizeOpenAIState(state, true)
	}

	// U7-C：用 UnmarshalWithExtras 同时拿 known 字段 + 上游 unknown 字段
	// （system_fingerprint / service_tier / logprobs / prompt_filter_results 等）
	// unknown 字段透传到第一条 emit 的 CanonicalEvent.Passthrough，由
	// ClientAdapter 在响应序列化时合并回客户端输出。
	var chunk openAIChatCompletionChunk
	var env PassthroughEnvelope
	if err := UnmarshalWithExtras([]byte(payload), &chunk, &env); err != nil {
		loss := newLossEntry(FeatureTextStreaming, DirectionUpstreamToCanonical, VerdictLossy, "malformed OpenAI SSE JSON chunk skipped")
		return nil, []ProtocolLossEntry{loss}
	}
	events, losses := openAIChunkToCanonicalEvents(chunk, state)
	// 把 unknown 字段附到第一条事件——避免重复 emit；如果本 chunk 没产
	// 出任何 event（如 usage-only 在 UsageEmitted 之后），unknown 字段丢失
	// 是可接受的（极罕见），不影响主路径正确性。
	if len(env.Extra) > 0 && len(events) > 0 {
		events[0].Passthrough = &env
	}
	return events, losses
}

func openAIChunkToCanonicalEvents(chunk openAIChatCompletionChunk, state *OpenAIUpstreamState) ([]CanonicalEvent, []ProtocolLossEntry) {
	ensureOpenAIState(state)
	if chunk.ID != "" {
		state.MessageID = chunk.ID
	}
	if chunk.Model != "" {
		state.Model = chunk.Model
	}

	var events []CanonicalEvent
	var losses []ProtocolLossEntry
	usageUpdated := updateOpenAIUsage(state, chunk.Usage)

	if len(chunk.Choices) == 0 {
		if usageUpdated && state.MessageStarted && !state.UsageEmitted {
			events = append(events, openAIUsageDeltaEvent(state))
			state.UsageEmitted = true
		}
		return events, losses
	}

	events = append(events, ensureOpenAIMessageStart(state)...)
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != nil && *choice.Delta.Content != "" {
			events = append(events, ensureOpenAITextBlock(state)...)
			state.AccumulatedContent += *choice.Delta.Content
			events = append(events, CanonicalEvent{
				Type:  "content_block_delta",
				Index: state.TextBlockIndex,
				Delta: &CanonicalContentDelta{Type: "text_delta", Text: *choice.Delta.Content},
			})
		}

		if len(choice.Delta.ToolCalls) > 0 {
			toolEvents, toolLosses := openAIToolCallDeltaEvents(choice.Delta.ToolCalls, state)
			events = append(events, toolEvents...)
			losses = append(losses, toolLosses...)
		}

		if choice.FinishReason != nil {
			state.RawFinishReason = *choice.FinishReason
			state.LastStopReason = mapOpenAIStopReason(*choice.FinishReason)
			events = appendOpenAIBlockStops(events, state)
			usage := (*CanonicalUsage)(nil)
			if usageUpdated && usageHasValue(state.AccumulatedUsage) {
				value := state.AccumulatedUsage
				usage = &value
				state.UsageEmitted = true
			}
			events = append(events, CanonicalEvent{
				Type:       "message_delta",
				Usage:      usage,
				StopReason: state.LastStopReason,
			})
			losses = append(losses, openAIStopLoss(*choice.FinishReason)...)
		}
	}

	if usageUpdated && usageHasValue(state.AccumulatedUsage) && !state.UsageEmitted {
		events = append(events, openAIUsageDeltaEvent(state))
		state.UsageEmitted = true
	}
	return events, losses
}

func ensureOpenAIState(state *OpenAIUpstreamState) {
	if state.ToolCalls == nil {
		state.ToolCalls = map[int]*OpenAIToolCallState{}
	}
}

func ensureOpenAIMessageStart(state *OpenAIUpstreamState) []CanonicalEvent {
	if state.MessageStarted {
		return nil
	}
	state.MessageStarted = true
	return []CanonicalEvent{{
		Type:      "message_start",
		MessageID: state.MessageID,
		Model:     state.Model,
	}}
}

func ensureOpenAITextBlock(state *OpenAIUpstreamState) []CanonicalEvent {
	if state.TextBlockStarted {
		state.TextBlockOpen = true
		return nil
	}
	state.TextBlockIndex = state.NextBlockIndex
	state.NextBlockIndex++
	state.TextBlockStarted = true
	state.TextBlockOpen = true
	block := CanonicalContentBlock{Type: "text"}
	return []CanonicalEvent{{
		Type:         "content_block_start",
		Index:        state.TextBlockIndex,
		ContentBlock: &block,
	}}
}

func openAIToolCallDeltaEvents(calls []openAIStreamToolCall, state *OpenAIUpstreamState) ([]CanonicalEvent, []ProtocolLossEntry) {
	var events []CanonicalEvent
	var losses []ProtocolLossEntry
	for _, delta := range calls {
		call := ensureOpenAIToolCallState(delta.Index, state)
		if delta.ID != "" {
			call.ID = delta.ID
			canonicalID, idLosses := canonicalOpenAICallID(delta.ID)
			call.CanonicalID = canonicalID
			losses = append(losses, idLosses...)
		}
		if delta.Type != "" {
			call.Type = delta.Type
		}
		if delta.Function.Name != nil {
			call.Name = *delta.Function.Name
		}
		if !call.Started {
			call.Started = true
			call.Open = true
			block := CanonicalContentBlock{Type: "tool_use", CallID: call.CanonicalID, Name: call.Name}
			events = append(events, CanonicalEvent{
				Type:         "content_block_start",
				Index:        call.BlockIndex,
				ContentBlock: &block,
			})
		}
		if delta.Function.Arguments != nil && *delta.Function.Arguments != "" {
			call.Arguments += *delta.Function.Arguments
			events = append(events, CanonicalEvent{
				Type:  "content_block_delta",
				Index: call.BlockIndex,
				Delta: &CanonicalContentDelta{
					Type:        "tool_input_delta",
					PartialJSON: json.RawMessage(strconv.Quote(*delta.Function.Arguments)),
				},
			})
		}
	}
	return events, losses
}

func ensureOpenAIToolCallState(index int, state *OpenAIUpstreamState) *OpenAIToolCallState {
	ensureOpenAIState(state)
	if call, ok := state.ToolCalls[index]; ok {
		return call
	}
	call := &OpenAIToolCallState{Index: index, BlockIndex: state.NextBlockIndex}
	state.NextBlockIndex++
	state.ToolCalls[index] = call
	return call
}

func appendOpenAIBlockStops(events []CanonicalEvent, state *OpenAIUpstreamState) []CanonicalEvent {
	if state.TextBlockOpen {
		events = append(events, CanonicalEvent{Type: "content_block_stop", Index: state.TextBlockIndex})
		state.TextBlockOpen = false
	}
	if len(state.ToolCalls) == 0 {
		return events
	}
	indexes := make([]int, 0, len(state.ToolCalls))
	for idx := range state.ToolCalls {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)
	for _, idx := range indexes {
		call := state.ToolCalls[idx]
		if call.Open {
			events = append(events, CanonicalEvent{Type: "content_block_stop", Index: call.BlockIndex})
			call.Open = false
		}
	}
	return events
}

func finalizeOpenAIState(state *OpenAIUpstreamState, fromDone bool) ([]CanonicalEvent, []ProtocolLossEntry) {
	ensureOpenAIState(state)
	if state.Terminated {
		if fromDone {
			loss := newLossEntry(FeatureTextStreaming, DirectionUpstreamToCanonical, VerdictLossy, "duplicate OpenAI [DONE] marker skipped")
			return nil, []ProtocolLossEntry{loss}
		}
		return nil, nil
	}
	var events []CanonicalEvent
	events = appendOpenAIBlockStops(events, state)
	if usageHasValue(state.AccumulatedUsage) && !state.UsageEmitted {
		events = append(events, openAIUsageDeltaEvent(state))
		state.UsageEmitted = true
	}
	state.Terminated = true
	// 观测 OpenAI prompt cache 命中（sonnet F4 修复）。OpenAI 只有 read 概念
	// (无 creation), 所以传 0 给 creation. Observe 内置 0/0 short-circuit。
	cachemetrics.ObserveByAccount(0, int64(state.AccumulatedUsage.CacheReadInputTokens), state.AccountID)
	events = append(events, CanonicalEvent{Type: "message_stop"})
	return events, nil
}

func openAIUsageDeltaEvent(state *OpenAIUpstreamState) CanonicalEvent {
	usage := state.AccumulatedUsage
	return CanonicalEvent{Type: "message_delta", Usage: &usage, StopReason: state.LastStopReason}
}

func updateOpenAIUsage(state *OpenAIUpstreamState, usage *openAIUsage) bool {
	if usage == nil {
		return false
	}
	state.AccumulatedUsage = usage.canonical()
	return true
}

func (u openAIUsage) canonical() CanonicalUsage {
	out := CanonicalUsage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
		TotalTokens:  u.TotalTokens,
	}
	if u.PromptTokensDetails != nil {
		// OpenAI 只暴露 read（命中）；creation 永远 0
		out.CacheReadInputTokens = u.PromptTokensDetails.CachedTokens
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.InputTokens + out.OutputTokens
	}
	return out
}

func usageHasValue(usage CanonicalUsage) bool {
	return usage.InputTokens != 0 || usage.OutputTokens != 0 || usage.TotalTokens != 0
}

func canonicalOpenAICallID(upstreamID string) (string, []ProtocolLossEntry) {
	if upstreamID == "" {
		return "", nil
	}
	callID, err := ToCanonicalCallID(upstreamID, UpstreamProtocolOpenAI)
	if err != nil {
		loss := newLossEntry(FeatureToolUse, DirectionUpstreamToCanonical, VerdictLossy, "malformed OpenAI tool call identifier")
		return "", []ProtocolLossEntry{loss}
	}
	return callID, nil
}

func mapOpenAIStopReason(reason string) CanonicalStopReason {
	switch reason {
	case "", "stop":
		return CanonicalStopEndTurn
	case "length":
		return CanonicalStopMaxTokens
	case "tool_calls", "function_call":
		return CanonicalStopToolUse
	case "content_filter":
		return CanonicalStopRefusal
	default:
		return CanonicalStopUnknown
	}
}

func openAIStopLoss(reason string) []ProtocolLossEntry {
	if reason == "" || mapOpenAIStopReason(reason) != CanonicalStopUnknown {
		return nil
	}
	return []ProtocolLossEntry{newLossEntry(FeatureMaxTokensFinishReason, DirectionUpstreamToCanonical, VerdictLossy, "unknown OpenAI finish_reason mapped to canonical unknown")}
}

func coerceOpenAISSEData(v any) ([]byte, error) {
	switch evt := v.(type) {
	case OpenAISSEEvent:
		return bytes.TrimSpace(evt.Data), nil
	case json.RawMessage:
		return extractOpenAISSEData(evt), nil
	case []byte:
		return extractOpenAISSEData(evt), nil
	case string:
		return extractOpenAISSEData([]byte(evt)), nil
	default:
		return nil, fmt.Errorf("proto: expected OpenAISSEEvent, []byte, or string")
	}
}

func extractOpenAISSEData(raw []byte) []byte {
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

func openAIResponseToCanonicalResponse(raw []byte) (CanonicalResponse, []ProtocolLossEntry, error) {
	// U7-C：non-streaming 响应路径也走 UnmarshalWithExtras，把顶层 unknown
	// 字段塞进 CanonicalResponse.Passthrough（system_fingerprint /
	// service_tier 等同样会出现在 chat.completion 响应顶层）。
	var resp openAIChatCompletionResponse
	var env PassthroughEnvelope
	if err := UnmarshalWithExtras(raw, &resp, &env); err != nil {
		return CanonicalResponse{}, nil, err
	}
	out := CanonicalResponse{ID: resp.ID, Model: resp.Model}
	if len(env.Extra) > 0 {
		out.Passthrough = &env
	}
	if resp.Usage != nil {
		out.Usage = resp.Usage.canonical()
	}
	if len(resp.Choices) == 0 {
		return out, nil, nil
	}

	choice := resp.Choices[0]
	out.StopReason = mapOpenAIStopReason(choice.FinishReason)
	losses := openAIStopLoss(choice.FinishReason)
	if text, textLosses := openAIResponseText(choice.Message.Content); text != "" || len(textLosses) > 0 {
		losses = append(losses, textLosses...)
		if text != "" {
			out.Content = append(out.Content, CanonicalContentBlock{Type: "text", Text: text})
		}
	}
	for _, tool := range choice.Message.ToolCalls {
		callID, idLosses := canonicalOpenAICallID(tool.ID)
		losses = append(losses, idLosses...)
		input := json.RawMessage(tool.Function.Arguments)
		if !json.Valid(input) {
			input = json.RawMessage(strconv.Quote(tool.Function.Arguments))
			losses = append(losses, newLossEntry(FeatureToolUse, DirectionUpstreamToCanonical, VerdictLossy, "OpenAI tool arguments were not valid JSON"))
		}
		out.Content = append(out.Content, CanonicalContentBlock{
			Type:   "tool_use",
			CallID: callID,
			Name:   tool.Function.Name,
			Input:  input,
		})
	}
	return out, losses, nil
}

func openAIResponseText(raw json.RawMessage) (string, []ProtocolLossEntry) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var parts []struct {
		Type    string `json:"type"`
		Text    string `json:"text,omitempty"`
		Refusal string `json:"refusal,omitempty"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		loss := newLossEntry(FeatureTextStreaming, DirectionUpstreamToCanonical, VerdictLossy, "OpenAI response content shape skipped")
		return "", []ProtocolLossEntry{loss}
	}
	var b strings.Builder
	var losses []ProtocolLossEntry
	for _, part := range parts {
		switch part.Type {
		case "text":
			b.WriteString(part.Text)
		case "refusal":
			b.WriteString(part.Refusal)
		default:
			losses = append(losses, newLossEntry(FeatureTextStreaming, DirectionUpstreamToCanonical, VerdictLossy, "unknown OpenAI response content part skipped"))
		}
	}
	return b.String(), losses
}

var _ UpstreamAdapter = (*OpenAIAdapter)(nil)
