// Phase C mock upstream emitter — produces a deterministic Anthropic SSE
// stream so the gateway can prove its Tx1→forward→Tx2 pipeline against
// real PostgreSQL without depending on Anthropic's network. Phase E will
// replace this with the real upstream client.
//
// SSE event shape mirrors backend/internal/gateway/forwarder_test.go so
// proto.AnthropicAdapter accepts the bytes and produces a non-zero
// UsageRecordDraft (TokensInput / TokensOutput populated).

package gatewayhttp

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// MockAnthropicUpstreamBytes returns a 4-event Anthropic SSE stream:
// message_start → content_block_delta → message_delta (usage) → message_stop.
// Caller passes inputTokens / outputTokens; the emitter encodes them onto
// the message_delta usage field, which the AnthropicAdapter extracts into
// the UsageRecordDraft.
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
