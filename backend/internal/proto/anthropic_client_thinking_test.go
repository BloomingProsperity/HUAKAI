package proto

import (
	"context"
	"strings"
	"testing"
)

// 判别测试:Claude 客户端回程必须渲染 thinking 块与 thinking 文本。
// 链路事实:上游 anthropic 适配器把 thinking_delta 统一转成 canonical
// reasoning_delta——此前客户端只认 thinking_delta(死分支),真实到来的
// reasoning_delta 掉 default 被丢 = Claude→Claude 中转 extended thinking
// 流式文本全丢;块 start 则被上游折成 unknown(已在上游侧修复)。
// Mutation guard: reasoning_delta 渲染撤掉 / 块 start 撤掉 → 对应断言红。
func TestAnthropicMessages_ThinkingRoundTripRendered(t *testing.T) {
	adapter := &AnthropicMessagesClient{}
	state := NewAnthropicMessagesStreamState()
	ctx := context.Background()

	if _, _, err := adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "message_start", MessageID: "msg_th", Model: "claude-opus",
	}, state); err != nil {
		t.Fatalf("message_start: %v", err)
	}

	// thinking 块 start → 渲染 {"type":"thinking"}
	chunks, losses, err := adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "content_block_start", Index: 0,
		ContentBlock: &CanonicalContentBlock{Type: "thinking"},
	}, state)
	if err != nil {
		t.Fatalf("thinking start: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("thinking start 不得记 loss: %+v", losses)
	}
	if len(chunks) == 0 || !strings.Contains(string(chunks[0]), `"thinking"`) {
		t.Fatalf("thinking 块 start 渲染缺失: %s", chunks)
	}

	// reasoning_delta(上游统一形态)→ thinking_delta 文本
	chunks, losses, err = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "content_block_delta", Index: 0,
		Delta: &CanonicalContentDelta{Type: "reasoning_delta", ReasoningText: "step 1"},
	}, state)
	if err != nil {
		t.Fatalf("reasoning_delta: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("reasoning_delta 不得再被丢: %+v", losses)
	}
	joined := ""
	for _, c := range chunks {
		joined += string(c)
	}
	if !strings.Contains(joined, "thinking_delta") || !strings.Contains(joined, "step 1") {
		t.Fatalf("thinking 文本未渲染: %s", joined)
	}

	// signature_delta → 渲染 signature
	chunks, _, err = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "content_block_delta", Index: 0,
		Delta: &CanonicalContentDelta{Type: "signature_delta", Signature: "sig_abc"},
	}, state)
	if err != nil {
		t.Fatalf("signature_delta: %v", err)
	}
	joined = ""
	for _, c := range chunks {
		joined += string(c)
	}
	if !strings.Contains(joined, "signature_delta") || !strings.Contains(joined, "sig_abc") {
		t.Fatalf("signature 未渲染: %s", joined)
	}

	// server_tool_use 块 start → 渲染 id/name
	chunks, _, err = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "content_block_start", Index: 1,
		ContentBlock: &CanonicalContentBlock{Type: "server_tool_use", CallID: "srvtoolu_1", Name: "web_search"},
	}, state)
	if err != nil {
		t.Fatalf("server_tool_use start: %v", err)
	}
	joined = ""
	for _, c := range chunks {
		joined += string(c)
	}
	if !strings.Contains(joined, "server_tool_use") || !strings.Contains(joined, "web_search") {
		t.Fatalf("server_tool_use 渲染缺失: %s", joined)
	}
}
