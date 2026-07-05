// Phase C mock upstream 发射器 —— 产生一段确定性的 Anthropic SSE
// 流,让 gateway 能针对真实 PostgreSQL 验证其 Tx1→forward→Tx2 流水线,
// 而无需依赖 Anthropic 的网络。Phase E 将用真实 upstream 客户端替换它。
//
// SSE 事件形态对齐 backend/internal/gateway/forwarder_test.go,以便
// anthropic.Adapter 接受这些字节并产出非零的
// UsageRecordDraft(TokensInput / TokensOutput 已填充)。

package gatewayhttp

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// MockAnthropicUpstreamBytes 返回一段含 4 个事件的 Anthropic SSE 流:
// message_start → content_block_delta → message_delta(usage)→ message_stop。
//调用方传入 inputTokens / outputTokens;发射器把它们编码进
// message_delta 的 usage 字段,anthropic.Adapter 再从中提取到
// UsageRecordDraft。
func MockAnthropicUpstreamBytes(messageID, model string, inputTokens, outputTokens int) []byte {
	var b bytes.Buffer
	writeSSE(&b, "message_start", map[string]any{
		"type":    "message_start",
		"message": map[string]any{"id": messageID, "model": model},
	})
	writeSSE(&b, "content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{"type": "text_delta", "text": "ok"},
	})
	writeSSE(&b, "message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn"},
		"usage": map[string]any{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
		},
	})
	writeSSE(&b, "message_stop", map[string]any{"type": "message_stop"})
	return b.Bytes()
}

func writeSSE(b *bytes.Buffer, eventType string, payload map[string]any) {
	if eventType != "" {
		fmt.Fprintf(b, "event: %s\n", eventType)
	}
	raw, _ := json.Marshal(payload)
	fmt.Fprintf(b, "data: %s\n\n", raw)
}
