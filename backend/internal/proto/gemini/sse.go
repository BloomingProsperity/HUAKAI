package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/cachemetrics"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// StopSafety 是 proto.HCSF 当前枚举未显式声明的 Gemini 安全停止哨兵值。
const StopSafety proto.CanonicalStopReason = "safety"

// Adapter 将 Gemini streamGenerateContent SSE 转换为 proto.HCSF 事件。
type Adapter struct{}

// SSEEvent 是 SSE 扫描器可传入的最小事件形态。
type SSEEvent struct {
	Type string `json:"type,omitempty"`
	Data []byte `json:"data,omitempty"`
	Done bool   `json:"done,omitempty"`
}

// StreamEnd 是调用方用于表示 Gemini SSE 连接自然关闭的哨兵。
type StreamEnd struct{}

// UpstreamState 累积 Gemini 流式响应的跨 chunk 状态。
type UpstreamState struct {
	MessageID             string
	Model                 string
	MessageStarted        bool
	Terminated            bool
	TextBlockOpen         bool
	TextBlockIndex        int
	NextBlockIndex        int
	GeneratedToolCallSeq  int
	AccumulatedContent    string
	AccumulatedUsage      proto.CanonicalUsage
	DeliveredChunkCount   int64
	CachedContentTokens   int
	UsageEmitted          bool
	LastStopReason        proto.CanonicalStopReason
	RawFinishReason       string
	SkippedExtraCandidate bool
	// AccountID（Track P）: forwarder 注入. cachemetrics.ObserveByAccount 用。
	// Gemini 自身 cache observation hook 后续 atomic 接入 (CachedContentTokens
	// 已 carry-over, 但终态触发点 future)。
	AccountID int64
	// PrefixHash（PASR-lite A4）: forwarder 注入 ForwardRequest.SessionHash;
	// finalize 时通过 ObserveByAccountWithPrefix 让 PASR observer 收反馈。
	PrefixHash string
	// M5b: TenantID 透传; observer 用 (TenantID, PrefixHash) 防跨租户混选。
	TenantID int64
}

type geminiGenerateContentResponse struct {
	Candidates     []geminiCandidate    `json:"candidates,omitempty"`
	ModelVersion   string               `json:"modelVersion,omitempty"`
	ResponseID     string               `json:"responseId,omitempty"`
	UsageMetadata  *geminiUsageMetadata `json:"usageMetadata,omitempty"`
	PromptFeedback map[string]any       `json:"promptFeedback,omitempty"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content,omitempty"`
	Index        int           `json:"index,omitempty"`
	FinishReason string        `json:"finishReason,omitempty"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts,omitempty"`
	Role  string       `json:"role,omitempty"`
}

type geminiPart struct {
	Text         *string             `json:"text,omitempty"`
	Thought      bool                `json:"thought,omitempty"`
	FunctionCall *geminiFunctionCall `json:"functionCall,omitempty"`
	InlineData   json.RawMessage     `json:"inlineData,omitempty"`
}

type geminiFunctionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name,omitempty"`
	Args json.RawMessage `json:"args,omitempty"`
}

type geminiUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount    int `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount         int `json:"totalTokenCount,omitempty"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
}

func (a *Adapter) CanonicalToProviderRequest(ctx context.Context, canonical *proto.HCSF) ([]byte, []proto.ProtocolLossEntry, error) {
	_ = ctx
	_ = canonical
	return nil, nil, proto.ErrNotImplemented
}

func (a *Adapter) ProviderResponseToCanonical(ctx context.Context, raw []byte) (*proto.HCSF, []proto.ProtocolLossEntry, error) {
	_ = ctx
	resp, losses, err := geminiResponseToCanonicalResponse(raw)
	if err != nil {
		return nil, losses, err
	}
	// P-0c-C D-FailLoud: 见 proto/openai/sse.go 同名注释。返回带 Version +
	// BufferedResponse 的最小 envelope，避免零值穿过边界。
	// envelope 仅适用 ValidateEnvelopeVersionGuard，不保证通过完整
	// ValidateEnvelope（RequestMeta 由 forwarder 层注入）。
	bufferedResp := resp
	env := &proto.HCSF{
		Version:          proto.HCSFVersion,
		BufferedResponse: &bufferedResp,
	}
	return env, losses, nil
}

func (a *Adapter) ProviderEventToCanonicalEvents(ctx context.Context, providerEvt any, state any) ([]any, []proto.ProtocolLossEntry, error) {
	_ = ctx
	st, ok := state.(*UpstreamState)
	if !ok {
		return nil, nil, fmt.Errorf("proto: expected *UpstreamState")
	}
	data, streamEnd, err := coerceGeminiSSEData(providerEvt)
	if err != nil {
		return nil, nil, err
	}
	if streamEnd {
		events, losses := finalizeGeminiState(st, true)
		return geminiEventsToAny(events), losses, nil
	}
	payload := strings.TrimSpace(string(data))
	if payload == "" {
		return nil, nil, nil
	}
	if payload == "[DONE]" {
		events, losses := finalizeGeminiState(st, true)
		return geminiEventsToAny(events), losses, nil
	}

	var chunk geminiGenerateContentResponse
	var env proto.PassthroughEnvelope
	if err := proto.UnmarshalWithExtras([]byte(payload), &chunk, &env); err != nil {
		loss := proto.NewLossEntry(proto.FeatureTextStreaming, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "malformed Gemini SSE JSON chunk skipped")
		return nil, []proto.ProtocolLossEntry{loss}, nil
	}
	events, losses := geminiChunkToCanonicalEvents(chunk, st)
	events = attachPassthrough(events, env)
	return geminiEventsToAny(events), losses, nil
}

func (a *Adapter) FinalizeUpstreamStream(ctx context.Context, state any) ([]any, error) {
	_ = ctx
	st, ok := state.(*UpstreamState)
	if !ok {
		return nil, fmt.Errorf("proto: expected *UpstreamState")
	}
	events, _ := finalizeGeminiState(st, false)
	return geminiEventsToAny(events), nil
}

func geminiChunkToCanonicalEvents(chunk geminiGenerateContentResponse, state *UpstreamState) ([]proto.CanonicalEvent, []proto.ProtocolLossEntry) {
	ensureGeminiState(state)
	if chunk.ResponseID != "" {
		state.MessageID = chunk.ResponseID
	}
	if chunk.ModelVersion != "" {
		state.Model = chunk.ModelVersion
	}
	updateGeminiUsage(state, chunk.UsageMetadata)

	if len(chunk.Candidates) == 0 {
		return nil, nil
	}

	var events []proto.CanonicalEvent
	var losses []proto.ProtocolLossEntry
	for _, candidate := range chunk.Candidates {
		if candidate.Index != 0 {
			state.SkippedExtraCandidate = true
			losses = append(losses, proto.NewLossEntry(proto.FeatureTextStreaming, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "non-primary Gemini candidate skipped"))
			continue
		}

		events = append(events, ensureGeminiMessageStart(state)...)
		for _, part := range candidate.Content.Parts {
			partEvents, partLosses := geminiPartToCanonicalEvents(part, state)
			events = append(events, partEvents...)
			losses = append(losses, partLosses...)
		}

		if candidate.FinishReason != "" {
			state.RawFinishReason = candidate.FinishReason
			state.LastStopReason = mapGeminiStopReason(candidate.FinishReason)
			events = appendGeminiOpenBlockStops(events, state)
			usage := (*proto.CanonicalUsage)(nil)
			if proto.UsageHasValue(state.AccumulatedUsage) {
				value := state.AccumulatedUsage
				usage = &value
				state.UsageEmitted = true
			}
			events = append(events, proto.CanonicalEvent{
				Type:       "message_delta",
				Usage:      usage,
				StopReason: state.LastStopReason,
			})
			losses = append(losses, geminiStopLoss(candidate.FinishReason)...)
		}
	}
	return events, losses
}

func geminiPartToCanonicalEvents(part geminiPart, state *UpstreamState) ([]proto.CanonicalEvent, []proto.ProtocolLossEntry) {
	var events []proto.CanonicalEvent
	var losses []proto.ProtocolLossEntry

	if part.FunctionCall != nil {
		events = appendGeminiOpenBlockStops(events, state)
		state.DeliveredChunkCount++
		index := state.NextBlockIndex
		state.NextBlockIndex++
		callID, idLosses := geminiCanonicalCallID(part.FunctionCall.ID, state)
		losses = append(losses, idLosses...)
		block := proto.CanonicalContentBlock{
			Type:   "tool_use",
			CallID: callID,
			Name:   part.FunctionCall.Name,
			Input:  normalizeGeminiFunctionArgs(part.FunctionCall.Args),
		}
		events = append(events,
			proto.CanonicalEvent{Type: "content_block_start", Index: index, ContentBlock: &block},
			proto.CanonicalEvent{Type: "content_block_stop", Index: index},
		)
		return events, losses
	}

	if len(bytes.TrimSpace(part.InlineData)) > 0 {
		// Emit the inline image as a canonical image block (mirrors the buffered
		// path in client.go) instead of dropping it. Streaming image generation
		// (e.g. gemini-2.5-flash-image via generateContent) previously lost the
		// image entirely while still billing the output tokens.
		imageIndex := state.NextBlockIndex
		state.NextBlockIndex++
		imageBlock := proto.CanonicalContentBlock{Type: "image", Image: cloneRaw(part.InlineData)}
		events = append(events,
			proto.CanonicalEvent{Type: "content_block_start", Index: imageIndex, ContentBlock: &imageBlock},
			proto.CanonicalEvent{Type: "content_block_stop", Index: imageIndex},
		)
		state.DeliveredChunkCount++
	}

	if part.Text == nil || *part.Text == "" {
		return events, losses
	}

	events = append(events, ensureGeminiTextBlock(state)...)
	if part.Thought {
		events = append(events, proto.CanonicalEvent{
			Type:  "content_block_delta",
			Index: state.TextBlockIndex,
			Delta: &proto.CanonicalContentDelta{Type: "reasoning_delta", ReasoningText: *part.Text},
		})
		return events, losses
	}

	state.AccumulatedContent += *part.Text
	state.DeliveredChunkCount++
	events = append(events, proto.CanonicalEvent{
		Type:  "content_block_delta",
		Index: state.TextBlockIndex,
		Delta: &proto.CanonicalContentDelta{Type: "text_delta", Text: *part.Text},
	})
	return events, losses
}

func ensureGeminiState(state *UpstreamState) {
	if state.NextBlockIndex < 0 {
		state.NextBlockIndex = 0
	}
}

func ensureGeminiMessageStart(state *UpstreamState) []proto.CanonicalEvent {
	if state.MessageStarted {
		return nil
	}
	state.MessageStarted = true
	return []proto.CanonicalEvent{{
		Type:      "message_start",
		MessageID: state.MessageID,
		Model:     state.Model,
	}}
}

func ensureGeminiTextBlock(state *UpstreamState) []proto.CanonicalEvent {
	if state.TextBlockOpen {
		return nil
	}
	state.TextBlockIndex = state.NextBlockIndex
	state.NextBlockIndex++
	state.TextBlockOpen = true
	block := proto.CanonicalContentBlock{Type: "text"}
	return []proto.CanonicalEvent{{
		Type:         "content_block_start",
		Index:        state.TextBlockIndex,
		ContentBlock: &block,
	}}
}

func appendGeminiOpenBlockStops(events []proto.CanonicalEvent, state *UpstreamState) []proto.CanonicalEvent {
	if state.TextBlockOpen {
		events = append(events, proto.CanonicalEvent{Type: "content_block_stop", Index: state.TextBlockIndex})
		state.TextBlockOpen = false
	}
	return events
}

func finalizeGeminiState(state *UpstreamState, fromSentinel bool) ([]proto.CanonicalEvent, []proto.ProtocolLossEntry) {
	ensureGeminiState(state)
	if state.Terminated {
		if fromSentinel {
			loss := proto.NewLossEntry(proto.FeatureTextStreaming, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "duplicate Gemini stream terminator skipped")
			return nil, []proto.ProtocolLossEntry{loss}
		}
		return nil, nil
	}

	var events []proto.CanonicalEvent
	events = appendGeminiOpenBlockStops(events, state)
	if proto.UsageHasValue(state.AccumulatedUsage) && !state.UsageEmitted {
		usage := state.AccumulatedUsage
		events = append(events, proto.CanonicalEvent{Type: "message_delta", Usage: &usage, StopReason: state.LastStopReason})
		state.UsageEmitted = true
	}
	state.Terminated = true
	// Gemini usageMetadata.cachedContentTokenCount 表示本请求从缓存读取的
	// prompt token。Gemini 没有 creation token 概念，creation 传 0。
	cachemetrics.ObserveByAccountWithPrefix(0, int64(state.CachedContentTokens), state.TenantID, state.AccountID, state.PrefixHash)
	events = append(events, proto.CanonicalEvent{Type: "message_stop"})
	return events, nil
}

func updateGeminiUsage(state *UpstreamState, usage *geminiUsageMetadata) bool {
	if usage == nil {
		return false
	}
	state.AccumulatedUsage = usage.canonical()
	state.CachedContentTokens = usage.CachedContentTokenCount
	return true
}

func (u geminiUsageMetadata) canonical() proto.CanonicalUsage {
	out := proto.CanonicalUsage{
		InputTokens:          u.PromptTokenCount,
		OutputTokens:         u.CandidatesTokenCount,
		TotalTokens:          u.TotalTokenCount,
		CacheReadInputTokens: u.CachedContentTokenCount,
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.InputTokens + out.OutputTokens
	}
	return out
}

func mapGeminiStopReason(reason string) proto.CanonicalStopReason {
	switch reason {
	case "STOP":
		return proto.CanonicalStopEndTurn
	case "MAX_TOKENS":
		return proto.CanonicalStopMaxTokens
	case "SAFETY":
		return StopSafety
	default:
		return proto.CanonicalStopUnknown
	}
}

func geminiStopLoss(reason string) []proto.ProtocolLossEntry {
	if reason == "" || reason == "STOP" || reason == "MAX_TOKENS" || reason == "SAFETY" {
		return nil
	}
	return []proto.ProtocolLossEntry{proto.NewLossEntry(proto.FeatureMaxTokensFinishReason, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "unknown Gemini finishReason mapped to canonical unknown")}
}

func geminiCanonicalCallID(upstreamID string, state *UpstreamState) (string, []proto.ProtocolLossEntry) {
	if upstreamID != "" {
		callID, err := proto.ToCanonicalCallID(upstreamID, proto.UpstreamProtocolGemini)
		if err != nil {
			loss := proto.NewLossEntry(proto.FeatureToolUse, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "malformed Gemini functionCall identifier")
			return "", []proto.ProtocolLossEntry{loss}
		}
		return callID, nil
	}
	state.GeneratedToolCallSeq++
	return fmt.Sprintf("call_%08x", state.GeneratedToolCallSeq), nil
}

func normalizeGeminiFunctionArgs(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return json.RawMessage(`{}`)
	}
	out := make([]byte, len(trimmed))
	copy(out, trimmed)
	return json.RawMessage(out)
}

func coerceGeminiSSEData(v any) ([]byte, bool, error) {
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
		return extractGeminiSSEData(evt), false, nil
	case []byte:
		return extractGeminiSSEData(evt), false, nil
	case string:
		return extractGeminiSSEData([]byte(evt)), false, nil
	case error:
		if errors.Is(evt, io.EOF) {
			return nil, true, nil
		}
		return nil, false, evt
	default:
		return nil, false, fmt.Errorf("proto: expected SSEEvent, []byte, string, or stream sentinel")
	}
}

func extractGeminiSSEData(raw []byte) []byte {
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

func geminiEventsToAny(events []proto.CanonicalEvent) []any {
	out := make([]any, len(events))
	for i := range events {
		out[i] = events[i]
	}
	return out
}

func geminiResponseToCanonicalResponse(raw []byte) (proto.CanonicalResponse, []proto.ProtocolLossEntry, error) {
	var resp geminiGenerateContentResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return proto.CanonicalResponse{}, nil, err
	}
	state := &UpstreamState{MessageID: resp.ResponseID, Model: resp.ModelVersion}
	updateGeminiUsage(state, resp.UsageMetadata)
	out := proto.CanonicalResponse{ID: resp.ResponseID, Model: resp.ModelVersion, Usage: state.AccumulatedUsage}
	if len(resp.Candidates) == 0 {
		return out, nil, nil
	}

	var losses []proto.ProtocolLossEntry
	candidate := resp.Candidates[0]
	out.StopReason = mapGeminiStopReason(candidate.FinishReason)
	losses = append(losses, geminiStopLoss(candidate.FinishReason)...)
	for _, part := range candidate.Content.Parts {
		if part.FunctionCall != nil {
			callID, idLosses := geminiCanonicalCallID(part.FunctionCall.ID, state)
			losses = append(losses, idLosses...)
			out.Content = append(out.Content, proto.CanonicalContentBlock{
				Type:   "tool_use",
				CallID: callID,
				Name:   part.FunctionCall.Name,
				Input:  normalizeGeminiFunctionArgs(part.FunctionCall.Args),
			})
			continue
		}
		if part.Text != nil && *part.Text != "" {
			if part.Thought {
				out.Content = append(out.Content, proto.CanonicalContentBlock{Type: "reasoning_summary", ReasoningSummary: *part.Text})
			} else {
				out.Content = append(out.Content, proto.CanonicalContentBlock{Type: "text", Text: *part.Text})
			}
		}
		if len(bytes.TrimSpace(part.InlineData)) > 0 {
			// 同 streaming 路(bb9d4d24):buffered 响应同样保留生成图。此前这里只记
			// lossy 丢图 = 非流式 generateContent 出图请求计了 output token 却收不到图。
			out.Content = append(out.Content, proto.CanonicalContentBlock{Type: "image", Image: cloneRaw(part.InlineData)})
		}
	}
	return out, losses, nil
}

var _ proto.UpstreamAdapter = (*Adapter)(nil)
