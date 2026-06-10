package proto

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// 判别测试:Responses 流式跨协议中转必须把 canonical tool_use 渲染成
// function_call 事件序(added → arguments.delta* → arguments.done → item.done),
// 形状对齐 buffered 渲染器(id="fc_"+call_id)。此前 tool_use/input_json_delta
// 只记 d11_pending loss 不发 chunk = Codex CLI 走 Claude 池时流式响应里所有
// function call 静默消失,agent 循环断裂(buffered 同请求却正常)。
// Mutation guard: 任一事件不发 / arguments 不累积 → 对应断言红。
func TestOpenAIResponses_D11_FunctionCallLifecycle_DeltaPattern(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	state := NewOpenAIResponsesStreamState()
	ctx := context.Background()

	if _, _, err := adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "message_start", MessageID: "resp_t", Model: "claude-opus",
	}, state); err != nil {
		t.Fatalf("message_start: %v", err)
	}

	// anthropic 模式:start 不带入参,由 input_json_delta 流上累积
	chunks, losses, err := adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type:  "content_block_start",
		Index: 0,
		ContentBlock: &CanonicalContentBlock{
			Type: "tool_use", CallID: "call_abc", Name: "lookup",
		},
	}, state)
	if err != nil {
		t.Fatalf("tool_use start: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("tool_use start 不得再记 loss 丢弃: %+v", losses)
	}
	joined := string(joinChunks(chunks))
	if !strings.Contains(joined, "response.output_item.added") ||
		!strings.Contains(joined, `"function_call"`) ||
		!strings.Contains(joined, `"fc_call_abc"`) ||
		!strings.Contains(joined, `"call_abc"`) ||
		!strings.Contains(joined, `"lookup"`) {
		t.Fatalf("function_call output_item.added 形状缺失: %s", joined)
	}

	// 两段 JSON 入参 delta
	for _, part := range []string{`{"city":`, `"sf"}`} {
		chunks, losses, err = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
			Type:  "content_block_delta",
			Index: 0,
			Delta: &CanonicalContentDelta{Type: "input_json_delta", PartialJSON: json.RawMessage(part)},
		}, state)
		if err != nil {
			t.Fatalf("input_json_delta: %v", err)
		}
		if len(losses) != 0 {
			t.Fatalf("input_json_delta 不得再记 loss 丢弃: %+v", losses)
		}
		if !strings.Contains(string(joinChunks(chunks)), "response.function_call_arguments.delta") {
			t.Fatalf("缺 arguments.delta 事件: %s", joinChunks(chunks))
		}
	}

	// stop:必须发 arguments.done(完整入参)+ output_item.done
	chunks, _, err = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "content_block_stop", Index: 0,
	}, state)
	if err != nil {
		t.Fatalf("content_block_stop: %v", err)
	}
	joined = string(joinChunks(chunks))
	if !strings.Contains(joined, "response.function_call_arguments.done") {
		t.Fatalf("缺 arguments.done: %s", joined)
	}
	if !strings.Contains(joined, `{\"city\":\"sf\"}`) && !strings.Contains(joined, `{"city":"sf"}`) {
		t.Fatalf("arguments 未按序累积出完整入参: %s", joined)
	}
	if !strings.Contains(joined, "response.output_item.done") {
		t.Fatalf("缺 output_item.done: %s", joined)
	}

	// message_stop 正常收尾
	chunks, _, err = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{Type: "message_stop"}, state)
	if err != nil || !strings.Contains(string(joinChunks(chunks)), "response.completed") {
		t.Fatalf("message_stop: %v %s", err, joinChunks(chunks))
	}
}

// gemini 模式:start 即携带完整入参、无 delta;stop 时入参必须完整出现在 done 里。
func TestOpenAIResponses_D11_FunctionCallLifecycle_FullInputAtStart(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	state := NewOpenAIResponsesStreamState()
	ctx := context.Background()

	if _, _, err := adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "message_start", MessageID: "resp_g", Model: "gemini-2.5-pro",
	}, state); err != nil {
		t.Fatalf("message_start: %v", err)
	}
	if _, _, err := adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type:  "content_block_start",
		Index: 0,
		ContentBlock: &CanonicalContentBlock{
			Type: "tool_use", CallID: "call_g1", Name: "search",
			Input: json.RawMessage(`{"q":"news"}`),
		},
	}, state); err != nil {
		t.Fatalf("tool_use start: %v", err)
	}
	chunks, _, err := adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "content_block_stop", Index: 0,
	}, state)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	joined := string(joinChunks(chunks))
	if !strings.Contains(joined, `{\"q\":\"news\"}`) && !strings.Contains(joined, `{"q":"news"}`) {
		t.Fatalf("start 携带的完整入参丢失: %s", joined)
	}
	if !strings.Contains(joined, "response.output_item.done") {
		t.Fatalf("缺 output_item.done: %s", joined)
	}
}

// 流断在 tool item 中途:Finalize 必须补 arguments.done + item.done 再 completed。
func TestOpenAIResponses_D11_FinalizeClosesOpenToolItem(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	state := NewOpenAIResponsesStreamState()
	ctx := context.Background()

	_, _, _ = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "message_start", MessageID: "resp_f", Model: "m",
	}, state)
	_, _, _ = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "content_block_start", Index: 0,
		ContentBlock: &CanonicalContentBlock{Type: "tool_use", CallID: "call_f", Name: "fn"},
	}, state)

	chunks, err := adapter.FinalizeClientStream(ctx, state)
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	joined := string(joinChunks(chunks))
	for _, want := range []string{"response.function_call_arguments.done", "response.output_item.done", "response.completed"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Finalize 缺 %s: %s", want, joined)
		}
	}
}

func joinChunks(chunks [][]byte) []byte {
	var out []byte
	for _, c := range chunks {
		out = append(out, c...)
	}
	return out
}
