package gemini

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"strings"
	"testing"
)

var _ proto.UpstreamAdapter = (*Adapter)(nil)

func TestGeminiAdapterHappyPathThreeChunks(t *testing.T) {
	const fixture = `
data: {"candidates":[{"content":{"parts":[{"text":"hi"}],"role":"model"},"index":0}],"modelVersion":"gemini-2.5-pro","responseId":"resp-1","usageMetadata":{"promptTokenCount":7,"totalTokenCount":7}}

data: {"candidates":[{"content":{"parts":[{"text":" there"}],"role":"model"},"index":0}],"modelVersion":"gemini-2.5-pro","responseId":"resp-1","usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":2,"totalTokenCount":9}}

data: {"candidates":[{"content":{"parts":[{"text":""}],"role":"model"},"index":0,"finishReason":"STOP"}],"modelVersion":"gemini-2.5-pro","responseId":"resp-1","usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":13,"totalTokenCount":20}}
`
	events, losses, state := runGeminiGoldenSSE(t, fixture)
	if len(losses) != 0 {
		t.Fatalf("happy path should not emit losses: %+v", losses)
	}
	wantTypes := []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	}
	if got := geminiEventTypes(events); strings.Join(got, ",") != strings.Join(wantTypes, ",") {
		t.Fatalf("event types mismatch\ngot  %v\nwant %v", got, wantTypes)
	}
	if events[0].MessageID != "resp-1" || events[0].Model != "gemini-2.5-pro" {
		t.Fatalf("message_start metadata mismatch: %+v", events[0])
	}
	if events[2].Delta == nil || events[2].Delta.Type != "text_delta" || events[2].Delta.Text != "hi" {
		t.Fatalf("first text delta mismatch: %+v", events[2].Delta)
	}
	if events[3].Delta == nil || events[3].Delta.Type != "text_delta" || events[3].Delta.Text != " there" {
		t.Fatalf("second text delta mismatch: %+v", events[3].Delta)
	}
	if state.AccumulatedContent != "hi there" {
		t.Fatalf("accumulated content mismatch: %q", state.AccumulatedContent)
	}
	usageEvent := events[5]
	if usageEvent.StopReason != proto.CanonicalStopEndTurn {
		t.Fatalf("STOP should map to end_turn, got %q", usageEvent.StopReason)
	}
	if usageEvent.Usage == nil || usageEvent.Usage.InputTokens != 7 || usageEvent.Usage.OutputTokens != 13 || usageEvent.Usage.TotalTokens != 20 {
		t.Fatalf("usage mismatch: %+v", usageEvent.Usage)
	}
	if !state.Terminated {
		t.Fatalf("FinalizeUpstreamStream should terminate Gemini stream state")
	}
}

func TestGeminiAdapterFunctionCallPartToToolUseBlock(t *testing.T) {
	adapter := &Adapter{}
	state := &UpstreamState{}
	payload := []byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"search","args":{"q":"x"}}}],"role":"model"},"index":0,"finishReason":"STOP"}],"modelVersion":"gemini-2.5-pro"}`)

	out, losses, err := adapter.ProviderEventToCanonicalEvents(context.Background(), payload, state)
	if err != nil {
		t.Fatalf("ProviderEventToCanonicalEvents: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("functionCall should not emit losses: %+v", losses)
	}
	events := geminiAnyToCanonicalEvents(t, out)
	var toolStart *proto.CanonicalEvent
	for i := range events {
		if events[i].Type == "content_block_start" && events[i].ContentBlock != nil && events[i].ContentBlock.Type == "tool_use" {
			toolStart = &events[i]
			break
		}
	}
	if toolStart == nil {
		t.Fatalf("tool_use content block start missing: %v", geminiEventTypes(events))
	}
	block := toolStart.ContentBlock
	if block.Name != "search" || !strings.HasPrefix(block.CallID, "call_") {
		t.Fatalf("tool block metadata mismatch: %+v", block)
	}
	if string(block.Input) != `{"q":"x"}` {
		t.Fatalf("tool input mismatch: %s", block.Input)
	}
}

func TestGeminiAdapterFinishReasonMappings(t *testing.T) {
	cases := []struct {
		reason string
		want   proto.CanonicalStopReason
	}{
		{reason: "STOP", want: proto.CanonicalStopEndTurn},
		{reason: "MAX_TOKENS", want: proto.CanonicalStopMaxTokens},
		{reason: "SAFETY", want: StopSafety},
	}
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			adapter := &Adapter{}
			state := &UpstreamState{}
			payload := []byte(fmt.Sprintf(`{"candidates":[{"content":{"parts":[]},"index":0,"finishReason":%q}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":4,"totalTokenCount":7}}`, tc.reason))
			out, losses, err := adapter.ProviderEventToCanonicalEvents(context.Background(), payload, state)
			if err != nil {
				t.Fatalf("ProviderEventToCanonicalEvents: %v", err)
			}
			if len(losses) != 0 {
				t.Fatalf("known finishReason should not emit losses: %+v", losses)
			}
			events := geminiAnyToCanonicalEvents(t, out)
			var got proto.CanonicalStopReason
			for _, ev := range events {
				if ev.Type == "message_delta" {
					got = ev.StopReason
				}
			}
			if got != tc.want {
				t.Fatalf("finishReason %q mapped to %q, want %q", tc.reason, got, tc.want)
			}
		})
	}
}

func TestGeminiAdapterMalformedJSONLineIgnored(t *testing.T) {
	const fixture = `
data: {"candidates":[{"content":{"parts":[{"text":"ok"}],"role":"model"},"index":0}]}

data: {"candidates":[{"content":
`
	events, losses, _ := runGeminiGoldenSSE(t, fixture)
	if len(losses) != 1 {
		t.Fatalf("malformed JSON should emit one loss entry, got %+v", losses)
	}
	if losses[0].Feature != string(proto.FeatureTextStreaming) {
		t.Fatalf("malformed JSON loss feature mismatch: %+v", losses[0])
	}
	if got := strings.Join(geminiEventTypes(events), ","); got != "message_start,content_block_start,content_block_delta,content_block_stop,message_stop" {
		t.Fatalf("malformed chunk should be skipped without crashing, got events %s", got)
	}
}

func TestGeminiAdapterEmptyCandidatesSkipped(t *testing.T) {
	adapter := &Adapter{}
	state := &UpstreamState{}
	out, losses, err := adapter.ProviderEventToCanonicalEvents(context.Background(), []byte(`{"modelVersion":"gemini-2.5-pro","usageMetadata":{"promptTokenCount":5,"cachedContentTokenCount":2,"totalTokenCount":5}}`), state)
	if err != nil {
		t.Fatalf("ProviderEventToCanonicalEvents: %v", err)
	}
	if len(out) != 0 || len(losses) != 0 {
		t.Fatalf("empty candidates should be skipped, out=%d losses=%+v", len(out), losses)
	}
	if state.MessageStarted {
		t.Fatalf("empty candidates should not synthesize message_start")
	}
	if state.AccumulatedUsage.InputTokens != 5 || state.CachedContentTokens != 2 {
		t.Fatalf("usage should still accumulate on metadata-only chunks: state=%+v", state)
	}
}

func TestGeminiAdapterMultiPartTextAndFunctionCall(t *testing.T) {
	adapter := &Adapter{}
	state := &UpstreamState{}
	payload := []byte(`{"candidates":[{"content":{"parts":[{"text":"check "},{"functionCall":{"id":"func_deadbeef","name":"lookup","args":{"id":42}}}],"role":"model"},"index":0,"finishReason":"STOP"}]}`)

	out, losses, err := adapter.ProviderEventToCanonicalEvents(context.Background(), payload, state)
	if err != nil {
		t.Fatalf("ProviderEventToCanonicalEvents: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("multi-part chunk should not emit losses: %+v", losses)
	}
	events := geminiAnyToCanonicalEvents(t, out)
	wantTypes := "message_start,content_block_start,content_block_delta,content_block_stop,content_block_start,content_block_stop,message_delta"
	if got := strings.Join(geminiEventTypes(events), ","); got != wantTypes {
		t.Fatalf("multi-part event order mismatch\ngot  %s\nwant %s", got, wantTypes)
	}
	if events[2].Delta == nil || events[2].Delta.Text != "check " {
		t.Fatalf("text delta mismatch: %+v", events[2].Delta)
	}
	if events[4].ContentBlock == nil || events[4].ContentBlock.Type != "tool_use" || events[4].ContentBlock.CallID != "call_deadbeef" {
		t.Fatalf("tool block mismatch: %+v", events[4].ContentBlock)
	}
}

func TestGeminiAdapterUsageCumulativeTracking(t *testing.T) {
	adapter := &Adapter{}
	state := &UpstreamState{}
	chunks := []struct {
		payload string
		input   int
		output  int
		total   int
		cached  int
	}{
		{payload: `{"candidates":[{"content":{"parts":[{"text":"a"}]},"index":0}],"usageMetadata":{"promptTokenCount":7,"cachedContentTokenCount":4,"totalTokenCount":7}}`, input: 7, output: 0, total: 7, cached: 4},
		{payload: `{"candidates":[{"content":{"parts":[{"text":"b"}]},"index":0}],"usageMetadata":{"promptTokenCount":7,"cachedContentTokenCount":4,"candidatesTokenCount":2,"totalTokenCount":9}}`, input: 7, output: 2, total: 9, cached: 4},
		{payload: `{"candidates":[{"content":{"parts":[{"text":""}]},"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":7,"cachedContentTokenCount":4,"candidatesTokenCount":5,"totalTokenCount":12}}`, input: 7, output: 5, total: 12, cached: 4},
	}
	for i, chunk := range chunks {
		if _, _, err := adapter.ProviderEventToCanonicalEvents(context.Background(), []byte(chunk.payload), state); err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
		if state.AccumulatedUsage.InputTokens != chunk.input || state.AccumulatedUsage.OutputTokens != chunk.output || state.AccumulatedUsage.TotalTokens != chunk.total || state.CachedContentTokens != chunk.cached {
			t.Fatalf("chunk %d usage mismatch: state=%+v want input=%d output=%d total=%d cached=%d", i, state, chunk.input, chunk.output, chunk.total, chunk.cached)
		}
	}
}

func TestGeminiAdapterStreamEndSentinelFinalizes(t *testing.T) {
	adapter := &Adapter{}
	state := &UpstreamState{}
	if _, _, err := adapter.ProviderEventToCanonicalEvents(context.Background(), []byte(`{"candidates":[{"content":{"parts":[{"text":"open"}]},"index":0}]}`), state); err != nil {
		t.Fatalf("ProviderEventToCanonicalEvents: %v", err)
	}
	out, losses, err := adapter.ProviderEventToCanonicalEvents(context.Background(), StreamEnd{}, state)
	if err != nil {
		t.Fatalf("sentinel finalize: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("first sentinel should not emit losses: %+v", losses)
	}
	events := geminiAnyToCanonicalEvents(t, out)
	if got := strings.Join(geminiEventTypes(events), ","); got != "content_block_stop,message_stop" {
		t.Fatalf("sentinel should close open block and stop message, got %s", got)
	}
	if !state.Terminated {
		t.Fatalf("sentinel should mark stream terminated")
	}
}

func runGeminiGoldenSSE(t *testing.T, fixture string) ([]proto.CanonicalEvent, []proto.ProtocolLossEntry, *UpstreamState) {
	t.Helper()
	adapter := &Adapter{}
	state := &UpstreamState{}
	var events []proto.CanonicalEvent
	var losses []proto.ProtocolLossEntry
	for _, payload := range scanGeminiGoldenData(t, fixture) {
		out, eventLosses, err := adapter.ProviderEventToCanonicalEvents(context.Background(), payload, state)
		if err != nil {
			t.Fatalf("ProviderEventToCanonicalEvents: %v", err)
		}
		events = append(events, geminiAnyToCanonicalEvents(t, out)...)
		losses = append(losses, eventLosses...)
	}
	final, err := adapter.FinalizeUpstreamStream(context.Background(), state)
	if err != nil {
		t.Fatalf("FinalizeUpstreamStream: %v", err)
	}
	events = append(events, geminiAnyToCanonicalEvents(t, final)...)
	return events, losses, state
}

func scanGeminiGoldenData(t *testing.T, fixture string) [][]byte {
	t.Helper()
	scanner := bufio.NewScanner(strings.NewReader(fixture))
	var events [][]byte
	var data strings.Builder
	flush := func() {
		if data.Len() == 0 {
			return
		}
		events = append(events, []byte(strings.TrimSpace(data.String())))
		data.Reset()
	}
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		part := strings.TrimPrefix(line, "data:")
		part = strings.TrimPrefix(part, " ")
		if data.Len() > 0 {
			data.WriteByte('\n')
		}
		data.WriteString(part)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan fixture: %v", err)
	}
	flush()
	return events
}

func geminiAnyToCanonicalEvents(t *testing.T, in []any) []proto.CanonicalEvent {
	t.Helper()
	out := make([]proto.CanonicalEvent, 0, len(in))
	for _, item := range in {
		ev, ok := item.(proto.CanonicalEvent)
		if !ok {
			t.Fatalf("unexpected event type %T", item)
		}
		out = append(out, ev)
	}
	return out
}

func geminiEventTypes(events []proto.CanonicalEvent) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Type)
	}
	return out
}

func TestGeminiAdapterFunctionCallInputIsValidJSON(t *testing.T) {
	adapter := &Adapter{}
	state := &UpstreamState{}
	out, _, err := adapter.ProviderEventToCanonicalEvents(context.Background(), []byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{"id":42}}}]},"index":0}]}`), state)
	if err != nil {
		t.Fatalf("ProviderEventToCanonicalEvents: %v", err)
	}
	for _, ev := range geminiAnyToCanonicalEvents(t, out) {
		if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" && !json.Valid(ev.ContentBlock.Input) {
			t.Fatalf("tool input should be valid JSON: %s", ev.ContentBlock.Input)
		}
	}
}

// TestGeminiAdapterFunctionCallNonPrefixedIDPreserved 守 S2-6：当 Gemini functionCall *携带* id
// 但不带 func_ 前缀(真实 Gemini provider id 即如此)，provided-id 分支必须保留成非空 canonical id，
// 而不是丢成空串。空分支(无 id → 合成 call_%08x)本就处理良好，唯独 provided-id 分支曾返回 ""。
// Mutation: 把 geminiCanonicalCallID 的 err 分支退回 `return "", []ProtocolLossEntry{loss}` →
// CallID 变空 → 此处 RED。
func TestGeminiAdapterFunctionCallNonPrefixedIDPreserved(t *testing.T) {
	adapter := &Adapter{}
	state := &UpstreamState{}
	out, _, err := adapter.ProviderEventToCanonicalEvents(context.Background(), []byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"id":"gemtool77","name":"lookup","args":{"id":42}}}],"role":"model"},"index":0,"finishReason":"STOP"}]}`), state)
	if err != nil {
		t.Fatalf("ProviderEventToCanonicalEvents: %v", err)
	}
	var tool *proto.CanonicalContentBlock
	for _, ev := range geminiAnyToCanonicalEvents(t, out) {
		if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
			tool = ev.ContentBlock
			break
		}
	}
	if tool == nil {
		t.Fatalf("expected a tool_use content block: %v", geminiAnyToCanonicalEvents(t, out))
	}
	if tool.CallID == "" {
		t.Fatalf("provided non-prefixed Gemini functionCall id was dropped to empty (the S2-6 defect)")
	}
	if tool.CallID != "call_gemtool77" {
		t.Fatalf("provided non-prefixed id should be preserved as call_<id>, got %q", tool.CallID)
	}
}
