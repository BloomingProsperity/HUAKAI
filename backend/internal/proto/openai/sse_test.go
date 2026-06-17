package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"strconv"
	"strings"
	"testing"
)

var _ proto.UpstreamAdapter = (*Adapter)(nil)

func TestOpenAIAdapterHappyPathGoldenSSE(t *testing.T) {
	const fixture = `
data: {"id":"chatcmpl-x","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}

data: {"id":"chatcmpl-x","choices":[{"index":0,"delta":{"content":" there"},"finish_reason":null}]}

data: {"id":"chatcmpl-x","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":13,"total_tokens":20}}

data: [DONE]
`
	events, losses, state := runOpenAIGoldenSSE(t, fixture)
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
	if got := openAIEventTypes(events); strings.Join(got, ",") != strings.Join(wantTypes, ",") {
		t.Fatalf("event types mismatch\ngot  %v\nwant %v", got, wantTypes)
	}
	if events[0].MessageID != "chatcmpl-x" || events[0].Model != "gpt-4o" {
		t.Fatalf("message_start metadata mismatch: %+v", events[0])
	}
	if events[2].Delta == nil || events[2].Delta.Text != "hi" {
		t.Fatalf("first text delta mismatch: %+v", events[2].Delta)
	}
	if events[3].Delta == nil || events[3].Delta.Text != " there" {
		t.Fatalf("second text delta mismatch: %+v", events[3].Delta)
	}
	if state.AccumulatedContent != "hi there" {
		t.Fatalf("accumulated content mismatch: %q", state.AccumulatedContent)
	}
	usageEvent := events[5]
	if usageEvent.StopReason != proto.CanonicalStopEndTurn {
		t.Fatalf("stop finish_reason should map to end_turn, got %q", usageEvent.StopReason)
	}
	if usageEvent.Usage == nil || usageEvent.Usage.InputTokens != 7 || usageEvent.Usage.OutputTokens != 13 || usageEvent.Usage.TotalTokens != 20 {
		t.Fatalf("usage mismatch: %+v", usageEvent.Usage)
	}
	if !state.Terminated {
		t.Fatalf("[DONE] should terminate stream state")
	}
}

func TestOpenAIAdapterMalformedJSONLineIgnored(t *testing.T) {
	const fixture = `
data: {"id":"chatcmpl-bad","choices":[{"delta":{"content":"ok"}}]}

data: {"id":"chatcmpl-bad","choices":[{"delta":

data: [DONE]
`
	events, losses, _ := runOpenAIGoldenSSE(t, fixture)
	if len(losses) != 1 {
		t.Fatalf("malformed JSON should emit one loss entry, got %+v", losses)
	}
	if losses[0].Feature != string(proto.FeatureTextStreaming) {
		t.Fatalf("malformed JSON loss feature mismatch: %+v", losses[0])
	}
	if got := openAIEventTypes(events); strings.Join(got, ",") != "message_start,content_block_start,content_block_delta,content_block_stop,message_stop" {
		t.Fatalf("malformed chunk should be skipped without crashing, got events %v", got)
	}
}

func TestOpenAIAdapterChunkWithoutChoicesSkipped(t *testing.T) {
	adapter := &Adapter{}
	state := &UpstreamState{}
	out, losses, err := adapter.ProviderEventToCanonicalEvents(context.Background(), []byte(`{"id":"chatcmpl-x","object":"chat.completion.chunk","model":"gpt-4o"}`), state)
	if err != nil {
		t.Fatalf("ProviderEventToCanonicalEvents: %v", err)
	}
	if len(out) != 0 || len(losses) != 0 {
		t.Fatalf("chunk without choices should be skipped, out=%d losses=%+v", len(out), losses)
	}
	if state.MessageStarted {
		t.Fatalf("metadata-only chunk should not synthesize message_start")
	}
}

func TestOpenAIAdapterMultiToolCallDeltaAccumulation(t *testing.T) {
	const fixture = `
data: {"id":"chatcmpl-tools","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_abcd","type":"function","function":{"name":"search","arguments":"{\"q\""}},{"index":1,"id":"call_beef","type":"function","function":{"name":"lookup","arguments":"{\"id\""}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-tools","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"x\"}"}},{"index":1,"function":{"arguments":":42}"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-tools","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]
`
	events, losses, state := runOpenAIGoldenSSE(t, fixture)
	if len(losses) != 0 {
		t.Fatalf("tool stream should not emit losses: %+v", losses)
	}
	if len(state.ToolCalls) != 2 {
		t.Fatalf("expected two accumulated tool calls, got %d", len(state.ToolCalls))
	}
	if state.ToolCalls[0].CanonicalID != "call_abcd" || state.ToolCalls[0].Name != "search" || state.ToolCalls[0].Arguments != `{"q":"x"}` {
		t.Fatalf("tool call 0 mismatch: %+v", state.ToolCalls[0])
	}
	if state.ToolCalls[1].CanonicalID != "call_beef" || state.ToolCalls[1].Name != "lookup" || state.ToolCalls[1].Arguments != `{"id":42}` {
		t.Fatalf("tool call 1 mismatch: %+v", state.ToolCalls[1])
	}
	var starts, deltas, stops int
	var sawToolStop bool
	for _, ev := range events {
		switch ev.Type {
		case "content_block_start":
			if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
				starts++
			}
		case "content_block_delta":
			if ev.Delta != nil && ev.Delta.Type == "tool_input_delta" {
				deltas++
				if got := unquotePartialJSON(t, ev.Delta.PartialJSON); got == "" {
					t.Fatalf("tool delta should preserve partial JSON")
				}
			}
		case "content_block_stop":
			stops++
		case "message_delta":
			if ev.StopReason == proto.CanonicalStopToolUse {
				sawToolStop = true
			}
		}
	}
	if starts != 2 || deltas != 4 || stops != 2 || !sawToolStop {
		t.Fatalf("tool event counts mismatch: starts=%d deltas=%d stops=%d sawToolStop=%v events=%v", starts, deltas, stops, sawToolStop, openAIEventTypes(events))
	}
}

func TestOpenAIAdapterFinishReasonMappings(t *testing.T) {
	cases := []struct {
		reason string
		want   proto.CanonicalStopReason
	}{
		{reason: "stop", want: proto.CanonicalStopEndTurn},
		{reason: "length", want: proto.CanonicalStopMaxTokens},
		{reason: "tool_calls", want: proto.CanonicalStopToolUse},
	}
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			adapter := &Adapter{}
			state := &UpstreamState{}
			payload := []byte(fmt.Sprintf(`{"id":"chatcmpl-%s","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":%q}]}`, tc.reason, tc.reason))
			out, losses, err := adapter.ProviderEventToCanonicalEvents(context.Background(), payload, state)
			if err != nil {
				t.Fatalf("ProviderEventToCanonicalEvents: %v", err)
			}
			if len(losses) != 0 {
				t.Fatalf("known finish reason should not emit losses: %+v", losses)
			}
			events := anyToCanonicalEvents(t, out)
			var got proto.CanonicalStopReason
			for _, ev := range events {
				if ev.Type == "message_delta" {
					got = ev.StopReason
				}
			}
			if got != tc.want {
				t.Fatalf("finish_reason %q mapped to %q, want %q", tc.reason, got, tc.want)
			}
		})
	}
}

func TestOpenAIAdapterNativeFinishReasonSurfacesInChatSSE(t *testing.T) {
	upstream := &Adapter{}
	upstreamState := &UpstreamState{}
	out, losses, err := upstream.ProviderEventToCanonicalEvents(context.Background(),
		[]byte(`{"id":"chatcmpl-native-finish","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"content_filter"}]}`),
		upstreamState)
	if err != nil {
		t.Fatalf("ProviderEventToCanonicalEvents: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("content_filter finish reason should not emit provider losses: %+v", losses)
	}

	client := &proto.OpenAIChatClient{}
	clientState := proto.NewOpenAIChatStreamState()
	var chunks [][]byte
	for _, ev := range anyToCanonicalEvents(t, out) {
		next, _, err := client.CanonicalEventToClientChunk(context.Background(), &ev, clientState)
		if err != nil {
			t.Fatalf("CanonicalEventToClientChunk(%s): %v", ev.Type, err)
		}
		chunks = append(chunks, next...)
	}

	for _, chunk := range chunks {
		if strings.Contains(string(chunk), `"finish_reason":"content_filter"`) {
			if !strings.Contains(string(chunk), `"native_finish_reason":"content_filter"`) {
				t.Fatalf("finish chunk missing native_finish_reason: %s", chunk)
			}
			return
		}
	}
	t.Fatalf("finish chunk not emitted; chunks=%q", chunks)
}

func TestOpenAIAdapterEmptyStreamFinalize(t *testing.T) {
	adapter := &Adapter{}
	state := &UpstreamState{}
	out, err := adapter.FinalizeUpstreamStream(context.Background(), state)
	if err != nil {
		t.Fatalf("FinalizeUpstreamStream: %v", err)
	}
	events := anyToCanonicalEvents(t, out)
	if len(events) != 1 || events[0].Type != "message_stop" {
		t.Fatalf("empty stream should synthesize terminal sentinel, got %+v", events)
	}
	if !state.Terminated {
		t.Fatalf("FinalizeUpstreamStream should mark empty stream terminated")
	}
}

func TestOpenAIBufferedResponseHelperParsesUsageAndTools(t *testing.T) {
	raw := []byte(`{"id":"chatcmpl-full","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"done","tool_calls":[{"id":"call_abcd","type":"function","function":{"name":"search","arguments":"{\"q\":\"x\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`)
	resp, losses, err := openAIResponseToCanonicalResponse(raw)
	if err != nil {
		t.Fatalf("openAIResponseToCanonicalResponse: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("buffered helper should not emit losses: %+v", losses)
	}
	if resp.ID != "chatcmpl-full" || resp.Model != "gpt-4o" || resp.StopReason != proto.CanonicalStopToolUse {
		t.Fatalf("buffered metadata mismatch: %+v", resp)
	}
	if resp.Usage.InputTokens != 3 || resp.Usage.OutputTokens != 5 || resp.Usage.TotalTokens != 8 {
		t.Fatalf("buffered usage mismatch: %+v", resp.Usage)
	}
	if len(resp.Content) != 2 || resp.Content[0].Text != "done" || resp.Content[1].CallID != "call_abcd" || resp.Content[1].Name != "search" {
		t.Fatalf("buffered content mismatch: %+v", resp.Content)
	}
	if !json.Valid(resp.Content[1].Input) {
		t.Fatalf("tool input should be valid JSON: %s", resp.Content[1].Input)
	}
}

// TestOpenAIBufferedResponseParsesReasoningTokens 守 S2-163-fu: o1/o3 的
// completion_tokens_details.reasoning_tokens 必须映射进 CanonicalUsage.ReasoningTokens
// 供 token 交叉校验扣除。Mutation: 去掉 canonical() 里的 CompletionTokensDetails 映射 →
// ReasoningTokens=0 → RED。
func TestOpenAIBufferedResponseParsesReasoningTokens(t *testing.T) {
	raw := []byte(`{"id":"chatcmpl-o1","object":"chat.completion","model":"o1","choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":300,"total_tokens":310,"completion_tokens_details":{"reasoning_tokens":200}}}`)
	resp, _, err := openAIResponseToCanonicalResponse(raw)
	if err != nil {
		t.Fatalf("openAIResponseToCanonicalResponse: %v", err)
	}
	if resp.Usage.OutputTokens != 300 {
		t.Fatalf("OutputTokens=%d want 300 (completion_tokens unchanged)", resp.Usage.OutputTokens)
	}
	if resp.Usage.ReasoningTokens != 200 {
		t.Fatalf("ReasoningTokens=%d want 200 (parsed from completion_tokens_details)", resp.Usage.ReasoningTokens)
	}
}

// TestOpenAIAdapterReasoningContentEmitsReasoningDelta 守住 ADP-1: 上游
// reasoning_content 必须投影为 canonical reasoning_delta, 且不污染答案正文。
// Mutation: 删 reasoning 处理分支 → 无 reasoning_delta 事件 → 红。
func TestOpenAIAdapterReasoningContentEmitsReasoningDelta(t *testing.T) {
	adapter := &Adapter{}
	state := &UpstreamState{}
	payload := []byte(`{"id":"chatcmpl-r","object":"chat.completion.chunk","model":"deepseek-r1","choices":[{"index":0,"delta":{"reasoning_content":"check hidden sum 2+2=4"},"finish_reason":null}]}`)
	out, losses, err := adapter.ProviderEventToCanonicalEvents(context.Background(), payload, state)
	if err != nil {
		t.Fatalf("ProviderEventToCanonicalEvents: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("reasoning projection should not emit losses: %+v", losses)
	}
	events := anyToCanonicalEvents(t, out)
	var reasoning *proto.CanonicalContentDelta
	for i := range events {
		if events[i].Type == "content_block_delta" && events[i].Delta != nil && events[i].Delta.Type == "reasoning_delta" {
			reasoning = events[i].Delta
		}
	}
	if reasoning == nil {
		t.Fatalf("expected a reasoning_delta event, got %v", openAIEventTypes(events))
	}
	if reasoning.ReasoningText != "check hidden sum 2+2=4" {
		t.Fatalf("reasoning text mismatch: %q", reasoning.ReasoningText)
	}
	if reasoning.Text != "" {
		t.Fatalf("reasoning delta must not populate answer Text: %q", reasoning.Text)
	}
	if state.AccumulatedContent != "" {
		t.Fatalf("reasoning must not accumulate into answer content: %q", state.AccumulatedContent)
	}
}

// TestOpenAIAdapterReasoningDoesNotPolluteAnswerText 自证测试: 同一 chunk 同时
// 含思维链与可见正文, 答案正文必须只含可见 content。Mutation: 把 reasoning 累加
// 进 AccumulatedContent → 正文变成 "thinking...Final 4" ≠ "Final 4" → 红。
func TestOpenAIAdapterReasoningDoesNotPolluteAnswerText(t *testing.T) {
	adapter := &Adapter{}
	state := &UpstreamState{}
	payload := []byte(`{"id":"chatcmpl-r","object":"chat.completion.chunk","model":"deepseek-r1","choices":[{"index":0,"delta":{"reasoning_content":"thinking...","content":"Final 4"},"finish_reason":null}]}`)
	if _, _, err := adapter.ProviderEventToCanonicalEvents(context.Background(), payload, state); err != nil {
		t.Fatalf("ProviderEventToCanonicalEvents: %v", err)
	}
	if state.AccumulatedContent != "Final 4" {
		t.Fatalf("answer content should exclude reasoning, got %q", state.AccumulatedContent)
	}
}

// TestOpenAIAdapterRefusalEmitsTextDeltaAndAccumulates 守住 ADP-4: 流式 refusal
// 必须 emit 为 text_delta 并累加进正文(对齐非流式)。Mutation: 不读 Delta.Refusal
// (改动前现状) → 无 text_delta、正文为空 → 红。
func TestOpenAIAdapterRefusalEmitsTextDeltaAndAccumulates(t *testing.T) {
	adapter := &Adapter{}
	state := &UpstreamState{}
	payload := []byte(`{"id":"chatcmpl-x","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"refusal":"I cannot assist with that."},"finish_reason":null}]}`)
	out, losses, err := adapter.ProviderEventToCanonicalEvents(context.Background(), payload, state)
	if err != nil {
		t.Fatalf("ProviderEventToCanonicalEvents: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("refusal emit should not emit losses: %+v", losses)
	}
	events := anyToCanonicalEvents(t, out)
	var refusalText string
	for i := range events {
		if events[i].Type == "content_block_delta" && events[i].Delta != nil && events[i].Delta.Type == "text_delta" {
			refusalText = events[i].Delta.Text
		}
	}
	if refusalText != "I cannot assist with that." {
		t.Fatalf("refusal text not emitted as text_delta: %q", refusalText)
	}
	if state.AccumulatedContent != "I cannot assist with that." {
		t.Fatalf("refusal should accumulate into content (align non-streaming): %q", state.AccumulatedContent)
	}
}

// TestOpenAIAdapterRefusalFinishReasonMapsToCanonicalRefusal 守住 finish_reason
// "refusal" 映射为 canonical refusal 且不误报 unknown-reason loss。Mutation: 删
// refusal case → 落 unknown → StopUnknown + openAIStopLoss 产 loss → 红。
func TestOpenAIAdapterRefusalFinishReasonMapsToCanonicalRefusal(t *testing.T) {
	adapter := &Adapter{}
	state := &UpstreamState{}
	payload := []byte(`{"id":"chatcmpl-x","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"refusal"}]}`)
	out, losses, err := adapter.ProviderEventToCanonicalEvents(context.Background(), payload, state)
	if err != nil {
		t.Fatalf("ProviderEventToCanonicalEvents: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("refusal finish reason is known, should not emit unknown-reason loss: %+v", losses)
	}
	events := anyToCanonicalEvents(t, out)
	var got proto.CanonicalStopReason
	for _, ev := range events {
		if ev.Type == "message_delta" {
			got = ev.StopReason
		}
	}
	if got != proto.CanonicalStopRefusal {
		t.Fatalf("finish_reason refusal mapped to %q, want refusal", got)
	}
}

// TestOpenAIAdapterReasoningContentRefusalEmitOrder 锁定同一 chunk 内的 emit 顺序:
// reasoning_delta(不累正文) → content text_delta(累) → refusal text_delta(累)。
// Mutation: 顺序错乱或把 reasoning 计入正文 → seq 或 AccumulatedContent 不符 → 红。
func TestOpenAIAdapterReasoningContentRefusalEmitOrder(t *testing.T) {
	adapter := &Adapter{}
	state := &UpstreamState{}
	payload := []byte(`{"id":"chatcmpl-x","object":"chat.completion.chunk","model":"deepseek-r1","choices":[{"index":0,"delta":{"reasoning_content":"R","content":"C","refusal":"F"},"finish_reason":null}]}`)
	out, _, err := adapter.ProviderEventToCanonicalEvents(context.Background(), payload, state)
	if err != nil {
		t.Fatalf("ProviderEventToCanonicalEvents: %v", err)
	}
	events := anyToCanonicalEvents(t, out)
	var seq []string
	for _, ev := range events {
		if ev.Type == "content_block_delta" && ev.Delta != nil {
			switch ev.Delta.Type {
			case "reasoning_delta":
				seq = append(seq, "R:"+ev.Delta.ReasoningText)
			case "text_delta":
				seq = append(seq, "T:"+ev.Delta.Text)
			}
		}
	}
	want := []string{"R:R", "T:C", "T:F"}
	if strings.Join(seq, ",") != strings.Join(want, ",") {
		t.Fatalf("emit order mismatch: got %v want %v", seq, want)
	}
	if state.AccumulatedContent != "CF" {
		t.Fatalf("accumulated content should be content+refusal only (reasoning excluded): %q", state.AccumulatedContent)
	}
}

func runOpenAIGoldenSSE(t *testing.T, fixture string) ([]proto.CanonicalEvent, []proto.ProtocolLossEntry, *UpstreamState) {
	t.Helper()
	adapter := &Adapter{}
	state := &UpstreamState{}
	var events []proto.CanonicalEvent
	var losses []proto.ProtocolLossEntry
	for _, payload := range scanOpenAIGoldenData(t, fixture) {
		out, eventLosses, err := adapter.ProviderEventToCanonicalEvents(context.Background(), payload, state)
		if err != nil {
			t.Fatalf("ProviderEventToCanonicalEvents: %v", err)
		}
		events = append(events, anyToCanonicalEvents(t, out)...)
		losses = append(losses, eventLosses...)
	}
	final, err := adapter.FinalizeUpstreamStream(context.Background(), state)
	if err != nil {
		t.Fatalf("FinalizeUpstreamStream: %v", err)
	}
	events = append(events, anyToCanonicalEvents(t, final)...)
	return events, losses, state
}

func scanOpenAIGoldenData(t *testing.T, fixture string) [][]byte {
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
		if strings.HasPrefix(part, " ") {
			part = strings.TrimPrefix(part, " ")
		}
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

func anyToCanonicalEvents(t *testing.T, in []any) []proto.CanonicalEvent {
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

func openAIEventTypes(events []proto.CanonicalEvent) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Type)
	}
	return out
}

func unquotePartialJSON(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	value, err := strconv.Unquote(string(raw))
	if err != nil {
		t.Fatalf("partial JSON should be encoded as JSON string, got %s: %v", raw, err)
	}
	return value
}

// TestOpenAIBufferedToolCallNonPrefixedIDPreserved 守 S1-1：OpenAI 兼容上游(Mistral 9 字符等)
// 返回的无 call_ 前缀 tool_call id 在 buffered 路径上必须被保留成非空 canonical id，
// 而不是丢成空串(空 id 会让下游 Anthropic 客户端硬报错、OpenAI 客户端也硬报错)，并记一条 loss。
// Mutation: 把 canonicalOpenAICallID 的 err 分支退回 `return "", []ProtocolLossEntry{loss}` →
// CallID 变空 → 此处 RED。
func TestOpenAIBufferedToolCallNonPrefixedIDPreserved(t *testing.T) {
	raw := []byte(`{"id":"chatcmpl-mistral","object":"chat.completion","model":"mistral-large","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"9aBc12345","type":"function","function":{"name":"search","arguments":"{\"q\":\"x\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	resp, losses, err := openAIResponseToCanonicalResponse(raw)
	if err != nil {
		t.Fatalf("openAIResponseToCanonicalResponse: %v", err)
	}
	var tool *proto.CanonicalContentBlock
	for i := range resp.Content {
		if resp.Content[i].Type == "tool_use" {
			tool = &resp.Content[i]
			break
		}
	}
	if tool == nil {
		t.Fatalf("expected a tool_use block, got %+v", resp.Content)
	}
	if tool.CallID == "" {
		t.Fatalf("non-prefixed tool_call id was dropped to empty (the S1-1 defect)")
	}
	if tool.CallID != "call_9aBc12345" {
		t.Fatalf("non-prefixed id should be preserved as call_<id>, got %q", tool.CallID)
	}
	// 行为正确性以"保留为非空可用 id"为准，但仍应记录一条 loss 标注做了 canonical 合成。
	if len(losses) == 0 {
		t.Fatalf("synthesizing a non-canonical id should still record a protocol loss")
	}
}

// TestOpenAIStreamingToolCallNonPrefixedIDPreserved 守 S1-1 的 streaming 半：
// streaming delta 里无 call_ 前缀的 id 同样必须保留进 tool_use content_block_start 的 CallID，
// 否则 Anthropic streaming 客户端会发出 id="" 的 tool_use(静默损坏，永远无法关联 tool_result)。
// Mutation: 同上把 err 分支退回返回空 → content_block_start 的 CallID 变空 → RED。
func TestOpenAIStreamingToolCallNonPrefixedIDPreserved(t *testing.T) {
	name := "search"
	calls := []openAIStreamToolCall{{
		Index:    0,
		ID:       "9aBc12345",
		Type:     "function",
		Function: openAIStreamFunction{Name: &name},
	}}
	events, losses := openAIToolCallDeltaEvents(calls, &UpstreamState{})
	var start *proto.CanonicalEvent
	for i := range events {
		if events[i].Type == "content_block_start" && events[i].ContentBlock != nil && events[i].ContentBlock.Type == "tool_use" {
			start = &events[i]
			break
		}
	}
	if start == nil {
		t.Fatalf("expected a tool_use content_block_start, got %+v", events)
	}
	if start.ContentBlock.CallID == "" {
		t.Fatalf("streaming non-prefixed tool_call id was dropped to empty (the S1-1 defect)")
	}
	if start.ContentBlock.CallID != "call_9aBc12345" {
		t.Fatalf("streaming non-prefixed id should be preserved as call_<id>, got %q", start.ContentBlock.CallID)
	}
	if len(losses) == 0 {
		t.Fatalf("synthesizing a non-canonical streaming id should still record a protocol loss")
	}
}
