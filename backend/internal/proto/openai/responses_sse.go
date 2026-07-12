package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// ResponsesAdapter 将 OpenAI Responses API 的 SSE/JSON 响应转换为 HCSF。
type ResponsesAdapter struct{}

var _ proto.UpstreamAdapter = (*ResponsesAdapter)(nil)

func (a *ResponsesAdapter) CanonicalToProviderRequest(context.Context, *proto.HCSF) ([]byte, []proto.ProtocolLossEntry, error) {
	return nil, nil, proto.ErrNotImplemented
}

func (a *ResponsesAdapter) ProviderResponseToCanonical(_ context.Context, raw []byte) (*proto.HCSF, []proto.ProtocolLossEntry, error) {
	resp, losses, err := responsesJSONToCanonical(raw)
	if err != nil {
		return nil, losses, err
	}
	env := &proto.HCSF{
		Version:          proto.HCSFVersion,
		BufferedResponse: &resp,
	}
	return env, losses, nil
}

func (a *ResponsesAdapter) ProviderEventToCanonicalEvents(_ context.Context, providerEvt any, state any) ([]any, []proto.ProtocolLossEntry, error) {
	st, ok := state.(*ResponsesUpstreamState)
	if !ok {
		return nil, nil, fmt.Errorf("proto: expected *ResponsesUpstreamState")
	}
	data, err := coerceOpenAISSEData(providerEvt)
	if err != nil {
		return nil, nil, err
	}
	payload := strings.TrimSpace(string(data))
	if payload == "" {
		return nil, nil, nil
	}

	var evt responsesStreamEvent
	if err := json.Unmarshal([]byte(payload), &evt); err != nil {
		loss := proto.NewLossEntry(proto.FeatureTextStreaming, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "malformed OpenAI Responses SSE JSON event skipped")
		return nil, []proto.ProtocolLossEntry{loss}, nil
	}
	events, losses := responsesEventToCanonical(evt, st)
	out := make([]any, len(events))
	for i := range events {
		out[i] = events[i]
	}
	return out, losses, nil
}

func (a *ResponsesAdapter) FinalizeUpstreamStream(_ context.Context, state any) ([]any, error) {
	st, ok := state.(*ResponsesUpstreamState)
	if !ok {
		return nil, fmt.Errorf("proto: expected *ResponsesUpstreamState")
	}
	if st.Terminated {
		return nil, nil
	}
	var events []proto.CanonicalEvent
	events = append(events, closeOpenResponsesItems(st)...)
	if st.Started {
		events = append(events, proto.CanonicalEvent{Type: "message_stop"})
		st.Terminated = true
	}
	out := make([]any, len(events))
	for i := range events {
		out[i] = events[i]
	}
	return out, nil
}

func responsesEventToCanonical(evt responsesStreamEvent, st *ResponsesUpstreamState) ([]proto.CanonicalEvent, []proto.ProtocolLossEntry) {
	if st.Items == nil {
		st.Items = map[int]*responsesItemState{}
	}
	if st.Terminated {
		return nil, nil
	}

	switch evt.Type {
	case "response.created", "response.in_progress":
		return ensureResponsesMessageStart(st, evt.Response), nil
	case "response.output_item.added":
		item := responsesOutputItem{}
		if evt.Item != nil {
			item = *evt.Item
		}
		idx := responsesEventIndex(evt)
		stateItem := st.responsesItem(idx, item)
		events := ensureResponsesMessageStart(st, evt.Response)
		events = append(events, ensureResponsesItemStarted(stateItem)...)
		return events, nil
	case "response.content_part.added":
		if evt.Part == nil || evt.Part.Type != "output_text" {
			return nil, nil
		}
		idx := responsesEventIndex(evt)
		item := st.responsesItem(idx, responsesOutputItem{ID: evt.ItemID, Type: "message"})
		events := ensureResponsesMessageStart(st, evt.Response)
		events = append(events, ensureResponsesItemStarted(item)...)
		return events, nil
	case "response.output_text.delta":
		idx := responsesEventIndex(evt)
		item := st.responsesItem(idx, responsesOutputItem{ID: evt.ItemID, Type: "message"})
		events := ensureResponsesMessageStart(st, evt.Response)
		events = append(events, ensureResponsesItemStarted(item)...)
		if evt.Delta == "" {
			return events, nil
		}
		item.TextDeltaSeen = true
		item.Text.WriteString(evt.Delta)
		events = append(events, proto.CanonicalEvent{
			Type:  "content_block_delta",
			Index: item.Index,
			Delta: &proto.CanonicalContentDelta{Type: "text_delta", Text: evt.Delta},
		})
		return events, nil
	case "response.output_text.done":
		if evt.Text == "" {
			return nil, nil
		}
		idx := responsesEventIndex(evt)
		item := st.responsesItem(idx, responsesOutputItem{ID: evt.ItemID, Type: "message"})
		return responsesItemTextOnce(st, item, evt.Text), nil
	case "response.function_call_arguments.delta":
		idx := responsesEventIndex(evt)
		item := st.responsesItem(idx, responsesOutputItem{ID: evt.ItemID, Type: "function_call"})
		events := ensureResponsesMessageStart(st, evt.Response)
		events = append(events, ensureResponsesItemStarted(item)...)
		if evt.Delta == "" {
			return events, nil
		}
		item.ArgsDeltaSeen = true
		item.Arguments.WriteString(evt.Delta)
		events = append(events, proto.CanonicalEvent{
			Type:  "content_block_delta",
			Index: item.Index,
			Delta: &proto.CanonicalContentDelta{Type: "tool_input_delta", PartialJSON: json.RawMessage(evt.Delta)},
		})
		return events, nil
	case "response.function_call_arguments.done":
		idx := responsesEventIndex(evt)
		item := st.responsesItem(idx, responsesOutputItem{ID: evt.ItemID, Type: "function_call"})
		return responsesItemArgumentsOnce(st, item, evt.Arguments), nil
	case "response.reasoning_summary_text.delta":
		idx := responsesEventIndex(evt)
		item := st.responsesItem(idx, responsesOutputItem{ID: evt.ItemID, Type: "reasoning"})
		events := ensureResponsesMessageStart(st, evt.Response)
		events = append(events, ensureResponsesItemStarted(item)...)
		if evt.Delta == "" {
			return events, nil
		}
		item.ReasoningSeen = true
		item.ReasoningSummary.WriteString(evt.Delta)
		events = append(events, proto.CanonicalEvent{
			Type:  "content_block_delta",
			Index: item.Index,
			Delta: &proto.CanonicalContentDelta{Type: "reasoning_delta", ReasoningText: evt.Delta},
		})
		return events, nil
	case "response.output_item.done":
		item := responsesOutputItem{}
		if evt.Item != nil {
			item = *evt.Item
		}
		idx := responsesEventIndex(evt)
		stateItem := st.responsesItem(idx, item)
		events := ensureResponsesMessageStart(st, evt.Response)
		events = append(events, ensureResponsesItemStarted(stateItem)...)
		events = append(events, responsesItemDoneDeltas(st, stateItem, item)...)
		events = append(events, stopResponsesItem(stateItem)...)
		return events, nil
	case "response.completed", "response.incomplete":
		events := ensureResponsesMessageStart(st, evt.Response)
		if evt.Response != nil {
			events = append(events, responsesOutputDoneEvents(st, evt.Response.Output)...)
		}
		events = append(events, closeOpenResponsesItems(st)...)
		usage := responsesCanonicalUsage(nil)
		stopReason := proto.CanonicalStopEndTurn
		if evt.Response != nil {
			usage = responsesCanonicalUsage(evt.Response.Usage)
			stopReason = responsesStopReason(evt.Response, responsesHasOpenTool(st))
		}
		events = append(events, proto.CanonicalEvent{
			Type:       "message_delta",
			Usage:      usagePtrIfAny(usage),
			StopReason: stopReason,
		})
		events = append(events, proto.CanonicalEvent{Type: "message_stop"})
		st.Terminated = true
		return events, responsesStopLoss(evt.Response)
	case "response.failed", "response.cancelled":
		events := ensureResponsesMessageStart(st, evt.Response)
		events = append(events, closeOpenResponsesItems(st)...)
		events = append(events, proto.CanonicalEvent{Type: "message_delta", StopReason: proto.CanonicalStopUnknown})
		events = append(events, proto.CanonicalEvent{Type: "message_stop"})
		st.Terminated = true
		return events, []proto.ProtocolLossEntry{proto.NewLossEntry(proto.FeatureTextStreaming, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "OpenAI Responses stream ended without completed status")}
	case "response.error":
		loss := proto.NewLossEntry(proto.FeatureTextStreaming, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "OpenAI Responses stream emitted an error event")
		return nil, []proto.ProtocolLossEntry{loss}
	default:
		return nil, []proto.ProtocolLossEntry{proto.NewLossEntry(proto.FeatureTextStreaming, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "unknown OpenAI Responses SSE event skipped")}
	}
}

func ensureResponsesMessageStart(st *ResponsesUpstreamState, resp *responsesObject) []proto.CanonicalEvent {
	if resp != nil {
		if resp.ID != "" {
			st.ResponseID = resp.ID
		}
		if resp.Model != "" {
			st.Model = resp.Model
		}
	}
	if st.Started {
		return nil
	}
	st.Started = true
	return []proto.CanonicalEvent{{Type: "message_start", MessageID: st.ResponseID, Model: st.Model}}
}

func (st *ResponsesUpstreamState) responsesItem(index int, item responsesOutputItem) *responsesItemState {
	if st.Items == nil {
		st.Items = map[int]*responsesItemState{}
	}
	if existing, ok := st.Items[index]; ok {
		existing.merge(item)
		return existing
	}
	out := &responsesItemState{Index: index}
	out.merge(item)
	if out.Type == "" {
		out.Type = "message"
	}
	st.Items[index] = out
	return out
}

func (it *responsesItemState) merge(item responsesOutputItem) {
	if item.ID != "" {
		it.ID = item.ID
	}
	if item.Type != "" {
		it.Type = item.Type
	}
	if item.CallID != "" {
		it.CallID = item.CallID
	}
	if item.Name != "" {
		it.Name = item.Name
	}
	if item.EncryptedContent != "" {
		it.Signature = item.EncryptedContent
	}
}

func ensureResponsesItemStarted(item *responsesItemState) []proto.CanonicalEvent {
	if item == nil || item.Started {
		return nil
	}
	item.Started = true
	switch item.Type {
	case "function_call":
		return []proto.CanonicalEvent{{
			Type:  "content_block_start",
			Index: item.Index,
			ContentBlock: &proto.CanonicalContentBlock{
				Type:   "tool_use",
				CallID: firstNonEmptyResponseString(item.CallID, item.ID),
				Name:   item.Name,
			},
		}}
	case "reasoning":
		return []proto.CanonicalEvent{{
			Type:  "content_block_start",
			Index: item.Index,
			ContentBlock: &proto.CanonicalContentBlock{
				Type:      "thinking",
				Signature: item.Signature,
			},
		}}
	default:
		return []proto.CanonicalEvent{{
			Type:         "content_block_start",
			Index:        item.Index,
			ContentBlock: &proto.CanonicalContentBlock{Type: "text"},
		}}
	}
}

func stopResponsesItem(item *responsesItemState) []proto.CanonicalEvent {
	if item == nil || item.Stopped {
		return nil
	}
	item.Stopped = true
	return []proto.CanonicalEvent{{Type: "content_block_stop", Index: item.Index}}
}

func closeOpenResponsesItems(st *ResponsesUpstreamState) []proto.CanonicalEvent {
	if st == nil || len(st.Items) == 0 {
		return nil
	}
	var events []proto.CanonicalEvent
	for i := 0; i <= maxResponsesItemIndex(st.Items); i++ {
		if item := st.Items[i]; item != nil && item.Started && !item.Stopped {
			events = append(events, stopResponsesItem(item)...)
		}
	}
	return events
}

func responsesOutputDoneEvents(st *ResponsesUpstreamState, output []responsesOutputItem) []proto.CanonicalEvent {
	var events []proto.CanonicalEvent
	for i, item := range output {
		stateItem := st.responsesItem(i, item)
		events = append(events, ensureResponsesItemStarted(stateItem)...)
		events = append(events, responsesItemDoneDeltas(st, stateItem, item)...)
		events = append(events, stopResponsesItem(stateItem)...)
	}
	return events
}

func responsesItemDoneDeltas(st *ResponsesUpstreamState, item *responsesItemState, done responsesOutputItem) []proto.CanonicalEvent {
	switch firstNonEmptyResponseString(done.Type, item.Type) {
	case "function_call":
		return responsesItemArgumentsOnce(st, item, done.Arguments)
	case "reasoning":
		return responsesItemReasoningOnce(st, item, done.Summary, done.EncryptedContent)
	default:
		return responsesItemContentOnce(st, item, done.Content)
	}
}

func responsesItemTextOnce(st *ResponsesUpstreamState, item *responsesItemState, text string) []proto.CanonicalEvent {
	if item == nil || text == "" || item.TextDeltaSeen {
		return nil
	}
	events := ensureResponsesMessageStart(st, nil)
	events = append(events, ensureResponsesItemStarted(item)...)
	item.TextDeltaSeen = true
	item.Text.WriteString(text)
	events = append(events, proto.CanonicalEvent{
		Type:  "content_block_delta",
		Index: item.Index,
		Delta: &proto.CanonicalContentDelta{Type: "text_delta", Text: text},
	})
	return events
}

func responsesItemContentOnce(st *ResponsesUpstreamState, item *responsesItemState, content []responsesOutputContent) []proto.CanonicalEvent {
	if item == nil || item.TextDeltaSeen {
		return nil
	}
	var text strings.Builder
	for _, part := range content {
		if part.Type == "output_text" && part.Text != "" {
			text.WriteString(part.Text)
		}
	}
	return responsesItemTextOnce(st, item, text.String())
}

func responsesItemArgumentsOnce(st *ResponsesUpstreamState, item *responsesItemState, args string) []proto.CanonicalEvent {
	if item == nil || args == "" || item.ArgsDeltaSeen {
		return nil
	}
	events := ensureResponsesMessageStart(st, nil)
	events = append(events, ensureResponsesItemStarted(item)...)
	item.ArgsDeltaSeen = true
	item.Arguments.WriteString(args)
	events = append(events, proto.CanonicalEvent{
		Type:  "content_block_delta",
		Index: item.Index,
		Delta: &proto.CanonicalContentDelta{Type: "tool_input_delta", PartialJSON: json.RawMessage(args)},
	})
	return events
}

func responsesItemReasoningOnce(st *ResponsesUpstreamState, item *responsesItemState, summary []responsesReasoningPart, signature string) []proto.CanonicalEvent {
	if item == nil {
		return nil
	}
	if signature != "" {
		item.Signature = signature
	}
	if item.ReasoningSeen {
		return nil
	}
	var text strings.Builder
	for _, part := range summary {
		if part.Text != "" {
			text.WriteString(part.Text)
		}
	}
	if text.Len() == 0 {
		return nil
	}
	events := ensureResponsesMessageStart(st, nil)
	events = append(events, ensureResponsesItemStarted(item)...)
	item.ReasoningSeen = true
	item.ReasoningSummary.WriteString(text.String())
	events = append(events, proto.CanonicalEvent{
		Type:  "content_block_delta",
		Index: item.Index,
		Delta: &proto.CanonicalContentDelta{Type: "reasoning_delta", ReasoningText: text.String()},
	})
	return events
}

func responsesEventIndex(evt responsesStreamEvent) int {
	if evt.OutputIndex != nil {
		return *evt.OutputIndex
	}
	return 0
}

func maxResponsesItemIndex(items map[int]*responsesItemState) int {
	max := 0
	for idx := range items {
		if idx > max {
			max = idx
		}
	}
	return max
}

func responsesHasOpenTool(st *ResponsesUpstreamState) bool {
	if st == nil {
		return false
	}
	for _, item := range st.Items {
		if item != nil && item.Type == "function_call" {
			return true
		}
	}
	return false
}

func usagePtrIfAny(usage proto.CanonicalUsage) *proto.CanonicalUsage {
	if !proto.UsageHasValue(usage) && usage.ReasoningTokens == 0 {
		return nil
	}
	return &usage
}

func responsesCanonicalUsage(u *responsesUsage) proto.CanonicalUsage {
	if u == nil {
		return proto.CanonicalUsage{}
	}
	out := proto.CanonicalUsage{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		TotalTokens:  u.TotalTokens,
	}
	if u.InputTokensDetails != nil {
		out.CacheReadInputTokens = u.InputTokensDetails.CachedTokens
	}
	if u.OutputTokensDetails != nil {
		out.ReasoningTokens = u.OutputTokensDetails.ReasoningTokens
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.InputTokens + out.OutputTokens
	}
	return out
}

func responsesStopReason(resp *responsesObject, hasTool bool) proto.CanonicalStopReason {
	if resp == nil {
		return proto.CanonicalStopUnknown
	}
	if resp.IncompleteDetails != nil {
		switch resp.IncompleteDetails.Reason {
		case "max_output_tokens":
			return proto.CanonicalStopMaxTokens
		case "content_filter":
			return proto.CanonicalStopRefusal
		default:
			return proto.CanonicalStopUnknown
		}
	}
	if hasTool || responsesOutputHasTool(resp.Output) {
		return proto.CanonicalStopToolUse
	}
	switch resp.Status {
	case "", "completed":
		return proto.CanonicalStopEndTurn
	case "incomplete":
		return proto.CanonicalStopMaxTokens
	default:
		return proto.CanonicalStopUnknown
	}
}

func responsesStopLoss(resp *responsesObject) []proto.ProtocolLossEntry {
	if resp == nil || resp.IncompleteDetails == nil || responsesStopReason(resp, false) != proto.CanonicalStopUnknown {
		return nil
	}
	return []proto.ProtocolLossEntry{proto.NewLossEntry(proto.FeatureMaxTokensFinishReason, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "unknown OpenAI Responses incomplete reason mapped to canonical unknown")}
}

func responsesOutputHasTool(output []responsesOutputItem) bool {
	for _, item := range output {
		if item.Type == "function_call" {
			return true
		}
	}
	return false
}

func responsesJSONToCanonical(raw []byte) (proto.CanonicalResponse, []proto.ProtocolLossEntry, error) {
	var resp responsesObject
	var passthrough proto.PassthroughEnvelope
	if err := proto.UnmarshalWithExtras(raw, &resp, &passthrough); err != nil {
		return proto.CanonicalResponse{}, nil, err
	}
	out := proto.CanonicalResponse{
		ID:         resp.ID,
		Model:      resp.Model,
		Usage:      responsesCanonicalUsage(resp.Usage),
		StopReason: responsesStopReason(&resp, false),
	}
	if len(passthrough.Extra) > 0 {
		out.Passthrough = &passthrough
	}
	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" && part.Text != "" {
					out.Content = append(out.Content, proto.CanonicalContentBlock{Type: "text", Text: part.Text})
				}
			}
		case "function_call":
			args := json.RawMessage(item.Arguments)
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			out.Content = append(out.Content, proto.CanonicalContentBlock{
				Type:   "tool_use",
				CallID: firstNonEmptyResponseString(item.CallID, item.ID),
				Name:   item.Name,
				Input:  args,
			})
		case "reasoning":
			var summary strings.Builder
			for _, part := range item.Summary {
				summary.WriteString(part.Text)
			}
			out.Content = append(out.Content, proto.CanonicalContentBlock{
				Type:             "thinking",
				Thinking:         summary.String(),
				ReasoningSummary: summary.String(),
				Signature:        item.EncryptedContent,
			})
		}
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return out, []proto.ProtocolLossEntry{proto.NewLossEntry(proto.FeatureTextStreaming, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "OpenAI Responses buffered response carried an error")}, nil
	}
	if out.ID == "" && out.Model == "" && len(out.Content) == 0 && !proto.UsageHasValue(out.Usage) {
		return out, nil, errors.New("proto: openai responses response missing recognizable fields")
	}
	return out, responsesStopLoss(&resp), nil
}

func firstNonEmptyResponseString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
