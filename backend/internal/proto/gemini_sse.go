package proto

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// GeminiStopSafety 是 HCSF 当前枚举未显式声明的 Gemini 安全停止哨兵值。
const GeminiStopSafety CanonicalStopReason = "safety"

// GeminiAdapter 将 Gemini streamGenerateContent SSE 转换为 HCSF 事件。
type GeminiAdapter struct{}

// GeminiSSEEvent 是 SSE 扫描器可传入的最小事件形态。
type GeminiSSEEvent struct {
	Type string `json:"type,omitempty"`
	Data []byte `json:"data,omitempty"`
	Done bool   `json:"done,omitempty"`
}

// GeminiStreamEnd 是调用方用于表示 Gemini SSE 连接自然关闭的哨兵。
type GeminiStreamEnd struct{}

// GeminiUpstreamState 累积 Gemini 流式响应的跨 chunk 状态。
type GeminiUpstreamState struct {
	MessageID             string
	Model                 string
	MessageStarted        bool
	Terminated            bool
	TextBlockOpen         bool
	TextBlockIndex        int
	NextBlockIndex        int
	GeneratedToolCallSeq  int
	AccumulatedContent    string
	AccumulatedUsage      CanonicalUsage
	CachedContentTokens   int
	UsageEmitted          bool
	LastStopReason        CanonicalStopReason
	RawFinishReason       string
	SkippedExtraCandidate bool
	// AccountID（Track P）: forwarder 注入. cachemetrics.ObserveByAccount 用。
	// Gemini 自身 cache observation hook 后续 atomic 接入 (CachedContentTokens
	// 已 carry-over, 但终态触发点 future)。
	AccountID int64
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

func (a *GeminiAdapter) CanonicalToProviderRequest(ctx context.Context, canonical *HCSF) ([]byte, []ProtocolLossEntry, error) {
	_ = ctx
	_ = canonical
	return nil, nil, ErrNotImplemented
}

func (a *GeminiAdapter) ProviderResponseToCanonical(ctx context.Context, raw []byte) (*HCSF, []ProtocolLossEntry, error) {
	_ = ctx
	_, losses, err := geminiResponseToCanonicalResponse(raw)
	if err != nil {
		return nil, losses, err
	}
	losses = append(losses, newLossEntry(FeatureTextStreaming, DirectionUpstreamToCanonical, VerdictLossy, "HCSF envelope has no buffered response slot in this slice"))
	return &HCSF{}, losses, nil
}

func (a *GeminiAdapter) ProviderEventToCanonicalEvents(ctx context.Context, providerEvt any, state any) ([]any, []ProtocolLossEntry, error) {
	_ = ctx
	st, ok := state.(*GeminiUpstreamState)
	if !ok {
		return nil, nil, fmt.Errorf("proto: expected *GeminiUpstreamState")
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
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		loss := newLossEntry(FeatureTextStreaming, DirectionUpstreamToCanonical, VerdictLossy, "malformed Gemini SSE JSON chunk skipped")
		return nil, []ProtocolLossEntry{loss}, nil
	}
	events, losses := geminiChunkToCanonicalEvents(chunk, st)
	return geminiEventsToAny(events), losses, nil
}

func (a *GeminiAdapter) FinalizeUpstreamStream(ctx context.Context, state any) ([]any, error) {
	_ = ctx
	st, ok := state.(*GeminiUpstreamState)
	if !ok {
		return nil, fmt.Errorf("proto: expected *GeminiUpstreamState")
	}
	events, _ := finalizeGeminiState(st, false)
	return geminiEventsToAny(events), nil
}

func geminiChunkToCanonicalEvents(chunk geminiGenerateContentResponse, state *GeminiUpstreamState) ([]CanonicalEvent, []ProtocolLossEntry) {
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

	var events []CanonicalEvent
	var losses []ProtocolLossEntry
	for _, candidate := range chunk.Candidates {
		if candidate.Index != 0 {
			state.SkippedExtraCandidate = true
			losses = append(losses, newLossEntry(FeatureTextStreaming, DirectionUpstreamToCanonical, VerdictLossy, "non-primary Gemini candidate skipped"))
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
			usage := (*CanonicalUsage)(nil)
			if usageHasValue(state.AccumulatedUsage) {
				value := state.AccumulatedUsage
				usage = &value
				state.UsageEmitted = true
			}
			events = append(events, CanonicalEvent{
				Type:       "message_delta",
				Usage:      usage,
				StopReason: state.LastStopReason,
			})
			losses = append(losses, geminiStopLoss(candidate.FinishReason)...)
		}
	}
	return events, losses
}

func geminiPartToCanonicalEvents(part geminiPart, state *GeminiUpstreamState) ([]CanonicalEvent, []ProtocolLossEntry) {
	var events []CanonicalEvent
	var losses []ProtocolLossEntry

	if part.FunctionCall != nil {
		events = appendGeminiOpenBlockStops(events, state)
		index := state.NextBlockIndex
		state.NextBlockIndex++
		callID, idLosses := geminiCanonicalCallID(part.FunctionCall.ID, state)
		losses = append(losses, idLosses...)
		block := CanonicalContentBlock{
			Type:   "tool_use",
			CallID: callID,
			Name:   part.FunctionCall.Name,
			Input:  normalizeGeminiFunctionArgs(part.FunctionCall.Args),
		}
		events = append(events,
			CanonicalEvent{Type: "content_block_start", Index: index, ContentBlock: &block},
			CanonicalEvent{Type: "content_block_stop", Index: index},
		)
		return events, losses
	}

	if len(bytes.TrimSpace(part.InlineData)) > 0 {
		losses = append(losses, newLossEntry(FeatureImageOutput, DirectionUpstreamToCanonical, VerdictLossy, "Gemini inlineData output part skipped"))
	}

	if part.Text == nil || *part.Text == "" {
		return events, losses
	}

	events = append(events, ensureGeminiTextBlock(state)...)
	if part.Thought {
		events = append(events, CanonicalEvent{
			Type:  "content_block_delta",
			Index: state.TextBlockIndex,
			Delta: &CanonicalContentDelta{Type: "reasoning_delta", ReasoningText: *part.Text},
		})
		return events, losses
	}

	state.AccumulatedContent += *part.Text
	events = append(events, CanonicalEvent{
		Type:  "content_block_delta",
		Index: state.TextBlockIndex,
		Delta: &CanonicalContentDelta{Type: "text_delta", Text: *part.Text},
	})
	return events, losses
}

func ensureGeminiState(state *GeminiUpstreamState) {
	if state.NextBlockIndex < 0 {
		state.NextBlockIndex = 0
	}
}

func ensureGeminiMessageStart(state *GeminiUpstreamState) []CanonicalEvent {
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

func ensureGeminiTextBlock(state *GeminiUpstreamState) []CanonicalEvent {
	if state.TextBlockOpen {
		return nil
	}
	state.TextBlockIndex = state.NextBlockIndex
	state.NextBlockIndex++
	state.TextBlockOpen = true
	block := CanonicalContentBlock{Type: "text"}
	return []CanonicalEvent{{
		Type:         "content_block_start",
		Index:        state.TextBlockIndex,
		ContentBlock: &block,
	}}
}

func appendGeminiOpenBlockStops(events []CanonicalEvent, state *GeminiUpstreamState) []CanonicalEvent {
	if state.TextBlockOpen {
		events = append(events, CanonicalEvent{Type: "content_block_stop", Index: state.TextBlockIndex})
		state.TextBlockOpen = false
	}
	return events
}

func finalizeGeminiState(state *GeminiUpstreamState, fromSentinel bool) ([]CanonicalEvent, []ProtocolLossEntry) {
	ensureGeminiState(state)
	if state.Terminated {
		if fromSentinel {
			loss := newLossEntry(FeatureTextStreaming, DirectionUpstreamToCanonical, VerdictLossy, "duplicate Gemini stream terminator skipped")
			return nil, []ProtocolLossEntry{loss}
		}
		return nil, nil
	}

	var events []CanonicalEvent
	events = appendGeminiOpenBlockStops(events, state)
	if usageHasValue(state.AccumulatedUsage) && !state.UsageEmitted {
		usage := state.AccumulatedUsage
		events = append(events, CanonicalEvent{Type: "message_delta", Usage: &usage, StopReason: state.LastStopReason})
		state.UsageEmitted = true
	}
	state.Terminated = true
	events = append(events, CanonicalEvent{Type: "message_stop"})
	return events, nil
}

func updateGeminiUsage(state *GeminiUpstreamState, usage *geminiUsageMetadata) bool {
	if usage == nil {
		return false
	}
	state.AccumulatedUsage = usage.canonical()
	state.CachedContentTokens = usage.CachedContentTokenCount
	return true
}

func (u geminiUsageMetadata) canonical() CanonicalUsage {
	out := CanonicalUsage{
		InputTokens:  u.PromptTokenCount,
		OutputTokens: u.CandidatesTokenCount,
		TotalTokens:  u.TotalTokenCount,
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.InputTokens + out.OutputTokens
	}
	return out
}

func mapGeminiStopReason(reason string) CanonicalStopReason {
	switch reason {
	case "STOP":
		return CanonicalStopEndTurn
	case "MAX_TOKENS":
		return CanonicalStopMaxTokens
	case "SAFETY":
		return GeminiStopSafety
	default:
		return CanonicalStopUnknown
	}
}

func geminiStopLoss(reason string) []ProtocolLossEntry {
	if reason == "" || reason == "STOP" || reason == "MAX_TOKENS" || reason == "SAFETY" {
		return nil
	}
	return []ProtocolLossEntry{newLossEntry(FeatureMaxTokensFinishReason, DirectionUpstreamToCanonical, VerdictLossy, "unknown Gemini finishReason mapped to canonical unknown")}
}

func geminiCanonicalCallID(upstreamID string, state *GeminiUpstreamState) (string, []ProtocolLossEntry) {
	if upstreamID != "" {
		callID, err := ToCanonicalCallID(upstreamID, UpstreamProtocolGemini)
		if err != nil {
			loss := newLossEntry(FeatureToolUse, DirectionUpstreamToCanonical, VerdictLossy, "malformed Gemini functionCall identifier")
			return "", []ProtocolLossEntry{loss}
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
	case GeminiStreamEnd:
		return nil, true, nil
	case *GeminiStreamEnd:
		return nil, true, nil
	case GeminiSSEEvent:
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
		return nil, false, fmt.Errorf("proto: expected GeminiSSEEvent, []byte, string, or stream sentinel")
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

func geminiEventsToAny(events []CanonicalEvent) []any {
	out := make([]any, len(events))
	for i := range events {
		out[i] = events[i]
	}
	return out
}

func geminiResponseToCanonicalResponse(raw []byte) (CanonicalResponse, []ProtocolLossEntry, error) {
	var resp geminiGenerateContentResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return CanonicalResponse{}, nil, err
	}
	state := &GeminiUpstreamState{MessageID: resp.ResponseID, Model: resp.ModelVersion}
	updateGeminiUsage(state, resp.UsageMetadata)
	out := CanonicalResponse{ID: resp.ResponseID, Model: resp.ModelVersion, Usage: state.AccumulatedUsage}
	if len(resp.Candidates) == 0 {
		return out, nil, nil
	}

	var losses []ProtocolLossEntry
	candidate := resp.Candidates[0]
	out.StopReason = mapGeminiStopReason(candidate.FinishReason)
	losses = append(losses, geminiStopLoss(candidate.FinishReason)...)
	for _, part := range candidate.Content.Parts {
		if part.FunctionCall != nil {
			callID, idLosses := geminiCanonicalCallID(part.FunctionCall.ID, state)
			losses = append(losses, idLosses...)
			out.Content = append(out.Content, CanonicalContentBlock{
				Type:   "tool_use",
				CallID: callID,
				Name:   part.FunctionCall.Name,
				Input:  normalizeGeminiFunctionArgs(part.FunctionCall.Args),
			})
			continue
		}
		if part.Text != nil && *part.Text != "" {
			if part.Thought {
				out.Content = append(out.Content, CanonicalContentBlock{Type: "reasoning_summary", ReasoningSummary: *part.Text})
			} else {
				out.Content = append(out.Content, CanonicalContentBlock{Type: "text", Text: *part.Text})
			}
		}
		if len(bytes.TrimSpace(part.InlineData)) > 0 {
			losses = append(losses, newLossEntry(FeatureImageOutput, DirectionUpstreamToCanonical, VerdictLossy, "Gemini inlineData output part skipped"))
		}
	}
	return out, losses, nil
}

var _ UpstreamAdapter = (*GeminiAdapter)(nil)
