package openai

import (
	"context"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestResponsesAdapterTextSSELifecycle(t *testing.T) {
	const fixture = `
event: response.created
data: {"type":"response.created","response":{"id":"resp_text","model":"gpt-5.5","status":"in_progress"}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant"}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"hi"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":" there"}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_text","model":"gpt-5.5","status":"completed","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5},"output":[]}}
`
	events, losses, state := runResponsesGoldenSSE(t, fixture)
	if len(losses) != 0 {
		t.Fatalf("Responses 文本流不应产生 loss: %+v", losses)
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
	if events[0].MessageID != "resp_text" || events[0].Model != "gpt-5.5" {
		t.Fatalf("message_start 元数据错误: %+v", events[0])
	}
	if events[2].Delta == nil || events[2].Delta.Text != "hi" {
		t.Fatalf("首段文本 delta 错误: %+v", events[2].Delta)
	}
	if events[3].Delta == nil || events[3].Delta.Text != " there" {
		t.Fatalf("第二段文本 delta 错误: %+v", events[3].Delta)
	}
	usageEvent := events[5]
	if usageEvent.StopReason != proto.CanonicalStopEndTurn {
		t.Fatalf("stop_reason=%q want end_turn", usageEvent.StopReason)
	}
	if usageEvent.Usage == nil || usageEvent.Usage.InputTokens != 2 || usageEvent.Usage.OutputTokens != 3 || usageEvent.Usage.TotalTokens != 5 {
		t.Fatalf("usage 错误: %+v", usageEvent.Usage)
	}
	if !state.Terminated {
		t.Fatal("response.completed 必须终止 Responses upstream state")
	}
}

func TestResponsesAdapterFunctionCallLifecycle(t *testing.T) {
	const fixture = `
data: {"type":"response.created","response":{"id":"resp_tool","model":"gpt-5.5"}}

data: {"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup"}}

data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"{\"city\":"}

data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"\"sf\"}"}

data: {"type":"response.function_call_arguments.done","item_id":"fc_1","output_index":0,"arguments":"{\"city\":\"sf\"}"}

data: {"type":"response.output_item.done","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"city\":\"sf\"}"}}

data: {"type":"response.completed","response":{"id":"resp_tool","model":"gpt-5.5","status":"completed","output":[{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"city\":\"sf\"}"}]}}
`
	events, losses, _ := runResponsesGoldenSSE(t, fixture)
	if len(losses) != 0 {
		t.Fatalf("Responses function_call 流不应产生 loss: %+v", losses)
	}
	var starts, deltas, stops int
	var sawToolStop bool
	for _, ev := range events {
		switch ev.Type {
		case "content_block_start":
			if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
				starts++
				if ev.ContentBlock.CallID != "call_1" || ev.ContentBlock.Name != "lookup" {
					t.Fatalf("tool_use start 错误: %+v", ev.ContentBlock)
				}
			}
		case "content_block_delta":
			if ev.Delta != nil && ev.Delta.Type == "tool_input_delta" {
				deltas++
			}
		case "content_block_stop":
			stops++
		case "message_delta":
			if ev.StopReason == proto.CanonicalStopToolUse {
				sawToolStop = true
			}
		}
	}
	if starts != 1 || deltas != 2 || stops != 1 || !sawToolStop {
		t.Fatalf("tool lifecycle mismatch: starts=%d deltas=%d stops=%d sawToolStop=%v events=%v", starts, deltas, stops, sawToolStop, openAIEventTypes(events))
	}
}

func TestResponsesAdapterReasoningLifecycle(t *testing.T) {
	const fixture = `
data: {"type":"response.created","response":{"id":"resp_reasoning","model":"gpt-5.5"}}

data: {"type":"response.output_item.added","output_index":0,"item":{"id":"rs_1","type":"reasoning","encrypted_content":"sig_1"}}

data: {"type":"response.reasoning_summary_text.delta","item_id":"rs_1","output_index":0,"delta":"简短推理"}

data: {"type":"response.output_item.done","output_index":0,"item":{"id":"rs_1","type":"reasoning","encrypted_content":"sig_1","summary":[{"type":"summary_text","text":"简短推理"}]}}

data: {"type":"response.completed","response":{"id":"resp_reasoning","model":"gpt-5.5","status":"completed","output":[]}}
`
	events, losses, _ := runResponsesGoldenSSE(t, fixture)
	if len(losses) != 0 {
		t.Fatalf("Responses reasoning 流不应产生 loss: %+v", losses)
	}
	var sawReasoningStart, sawReasoningDelta bool
	for _, ev := range events {
		if ev.Type == "content_block_start" && ev.ContentBlock != nil && ev.ContentBlock.Type == "thinking" {
			sawReasoningStart = true
			if ev.ContentBlock.Signature != "sig_1" {
				t.Fatalf("reasoning signature=%q want sig_1", ev.ContentBlock.Signature)
			}
		}
		if ev.Type == "content_block_delta" && ev.Delta != nil && ev.Delta.Type == "reasoning_delta" {
			sawReasoningDelta = true
			if ev.Delta.ReasoningText != "简短推理" {
				t.Fatalf("reasoning delta=%q", ev.Delta.ReasoningText)
			}
		}
	}
	if !sawReasoningStart || !sawReasoningDelta {
		t.Fatalf("缺 reasoning 事件: start=%v delta=%v events=%v", sawReasoningStart, sawReasoningDelta, openAIEventTypes(events))
	}
}

func TestResponsesAdapterBufferedResponse(t *testing.T) {
	adapter := &ResponsesAdapter{}
	env, losses, err := adapter.ProviderResponseToCanonical(context.Background(), []byte(`{
		"id":"resp_buffered",
		"model":"gpt-5.5",
		"status":"completed",
		"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3},
		"output":[
			{"type":"message","content":[{"type":"output_text","text":"ok"}]},
			{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{\"q\":\"x\"}"},
			{"type":"reasoning","encrypted_content":"sig_1","summary":[{"type":"summary_text","text":"why"}]}
		]
	}`))
	if err != nil {
		t.Fatalf("ProviderResponseToCanonical: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("buffered response 不应产生 loss: %+v", losses)
	}
	if env.BufferedResponse == nil {
		t.Fatal("BufferedResponse nil")
	}
	resp := env.BufferedResponse
	if resp.ID != "resp_buffered" || resp.Model != "gpt-5.5" || resp.StopReason != proto.CanonicalStopToolUse {
		t.Fatalf("buffered 元数据错误: %+v", resp)
	}
	if resp.Usage.TotalTokens != 3 || len(resp.Content) != 3 {
		t.Fatalf("buffered 内容/usage 错误: usage=%+v content=%+v", resp.Usage, resp.Content)
	}
	if resp.Content[0].Type != "text" || resp.Content[0].Text != "ok" {
		t.Fatalf("text content 错误: %+v", resp.Content[0])
	}
	if resp.Content[1].Type != "tool_use" || resp.Content[1].CallID != "call_1" || resp.Content[1].Name != "lookup" {
		t.Fatalf("tool content 错误: %+v", resp.Content[1])
	}
	if resp.Content[2].Type != "thinking" || resp.Content[2].ReasoningSummary != "why" || resp.Content[2].Signature != "sig_1" {
		t.Fatalf("reasoning content 错误: %+v", resp.Content[2])
	}
}

func runResponsesGoldenSSE(t *testing.T, fixture string) ([]proto.CanonicalEvent, []proto.ProtocolLossEntry, *ResponsesUpstreamState) {
	t.Helper()
	adapter := &ResponsesAdapter{}
	state := &ResponsesUpstreamState{}
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
