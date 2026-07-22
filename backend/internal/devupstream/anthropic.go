// 开发上游模拟器生成确定性的 Anthropic SSE 流，供本地完整链路演练使用。
package devupstream

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// MockAnthropicUpstreamBytes 返回一段含 4 个事件的 Anthropic SSE 流:
// message_start → content_block_delta → message_delta(usage)→ message_stop。
// 调用方传入 inputTokens / outputTokens;发射器把它们编码进
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
