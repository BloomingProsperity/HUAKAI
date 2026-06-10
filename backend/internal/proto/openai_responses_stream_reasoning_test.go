package proto

import (
	"context"
	"strings"
	"testing"
)

// 判别测试:Responses 流式必须把上游 thinking 块渲染成 reasoning output item
// (added → reasoning_summary_text.delta → output_item.done 带 summary_text),
// 形状对齐 buffered 渲染器 openAIResponsesReasoningOutputItem。此前 thinking
// 块 start 掉 default 当 unknown 丢、reasoning_delta 同样丢 = 流式 reasoning
// 摘要全丢(buffered 同请求正常)。
// Mutation guard: 不发 item / 文本不累积 → 对应断言红。
func TestOpenAIResponses_ReasoningItemLifecycle(t *testing.T) {
	adapter := &OpenAIResponsesClient{}
	state := NewOpenAIResponsesStreamState()
	ctx := context.Background()

	if _, _, err := adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "message_start", MessageID: "resp_r", Model: "claude-opus",
	}, state); err != nil {
		t.Fatalf("message_start: %v", err)
	}

	chunks, losses, err := adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "content_block_start", Index: 0,
		ContentBlock: &CanonicalContentBlock{Type: "thinking"},
	}, state)
	if err != nil {
		t.Fatalf("thinking start: %v", err)
	}
	if len(losses) != 0 {
		t.Fatalf("thinking start 不得再记 loss 丢弃: %+v", losses)
	}
	joined := chunksJoined(chunks)
	if !strings.Contains(joined, "response.output_item.added") || !strings.Contains(joined, `"reasoning"`) {
		t.Fatalf("reasoning output_item.added 缺失: %s", joined)
	}

	for _, part := range []string{"think ", "hard"} {
		chunks, losses, err = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
			Type: "content_block_delta", Index: 0,
			Delta: &CanonicalContentDelta{Type: "reasoning_delta", ReasoningText: part},
		}, state)
		if err != nil {
			t.Fatalf("reasoning_delta: %v", err)
		}
		if len(losses) != 0 {
			t.Fatalf("reasoning_delta 不得再被丢: %+v", losses)
		}
		if !strings.Contains(chunksJoined(chunks), "response.reasoning_summary_text.delta") {
			t.Fatalf("缺 reasoning_summary_text.delta: %s", chunksJoined(chunks))
		}
	}

	// signature 累积进 encrypted_content
	if _, _, err = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "content_block_delta", Index: 0,
		Delta: &CanonicalContentDelta{Type: "signature_delta", Signature: "sig_xyz"},
	}, state); err != nil {
		t.Fatalf("signature_delta: %v", err)
	}

	chunks, _, err = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "content_block_stop", Index: 0,
	}, state)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	joined = chunksJoined(chunks)
	if !strings.Contains(joined, "response.output_item.done") {
		t.Fatalf("缺 reasoning output_item.done: %s", joined)
	}
	if !strings.Contains(joined, "think hard") {
		t.Fatalf("reasoning 文本未按序累积进 summary: %s", joined)
	}
	if !strings.Contains(joined, "sig_xyz") {
		t.Fatalf("signature 未进 encrypted_content: %s", joined)
	}

	// 后续 text 块正常(reasoning 已收尾不串扰)
	chunks, _, err = adapter.CanonicalEventToClientChunk(ctx, &CanonicalEvent{
		Type: "content_block_start", Index: 1,
		ContentBlock: &CanonicalContentBlock{Type: "text"},
	}, state)
	if err != nil || !strings.Contains(chunksJoined(chunks), "output_text") {
		t.Fatalf("reasoning 后的 text 块异常: %v %s", err, chunksJoined(chunks))
	}
}

func chunksJoined(chunks [][]byte) string {
	var b strings.Builder
	for _, c := range chunks {
		b.Write(c)
	}
	return b.String()
}
