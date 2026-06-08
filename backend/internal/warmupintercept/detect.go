// Package warmupintercept detects Claude Code "throwaway" requests and builds
// synthetic responses so they never reach the pooled upstream account.
//
// Faithfully ported from sub2api-latest:
//
//	backend/internal/handler/gateway_handler.go  lines 1604-1810
//
// Three intercept shapes (all must match exactly):
//  1. Connectivity probe   -- max_tokens==1, haiku model, non-stream, Claude Code UA
//  2. Suggestion mode      -- last user message text starts with "[SUGGESTION MODE:"
//  3. Warmup / title-gen   -- message contains title-generation prompt OR system has
//     "nalyze if this message indicates a new conversation topic..."
//
// The gate (warmup_intercept_enabled, default false) is opt-in; when OFF this
// package is a pure no-op from the caller's perspective.
package warmupintercept

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Kind is the type of intercepted request.
type Kind int

const (
	KindNone       Kind = iota
	KindConnProbe       // max_tokens=1 + haiku + non-stream (connectivity probe)
	KindSuggestion      // [SUGGESTION MODE: ...] in last user message
	KindWarmup          // title-generation / warmup prompt
)

// isHaikuModel returns true when model name contains "haiku" (case-insensitive).
// Source: gateway_handler.go:1614-1616
func isHaikuModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "haiku")
}

// isConnProbeShape returns true for the connectivity-probe shape:
// max_tokens==1 AND haiku model AND non-streaming.
// Source: gateway_handler.go:1619-1623
func isConnProbeShape(model string, maxTokens int, stream bool) bool {
	return maxTokens == 1 && isHaikuModel(model) && !stream
}

// Detect classifies a request into one of the three intercept kinds.
// All parameters come from the already-parsed request body -- no re-decode needed.
//
//   - isClaudeCodeUA: caller should pass IsClaudeCodeUserAgent(r.UserAgent())
//   - body: raw request body bytes (used for warmup/suggestion fast-path scan + structured check)
//   - model, maxTokens, stream: from the parsed top-level fields
//
// Source: gateway_handler.go:1626-1696
func Detect(isClaudeCodeUA bool, model string, maxTokens int, stream bool, body []byte) (Kind, bool) {
	// Shape 1: connectivity probe (requires Claude Code UA)
	// Source: gateway_handler.go:1634-1637
	if isClaudeCodeUA && isConnProbeShape(model, maxTokens, stream) {
		return KindConnProbe, true
	}

	// Fast-path: if body contains neither keyword, nothing further to check.
	// Source: gateway_handler.go:1639-1647
	bodyStr := string(body)
	hasSuggestion := strings.Contains(bodyStr, "[SUGGESTION MODE:")
	hasWarmup := strings.Contains(bodyStr, "title") || strings.Contains(bodyStr, "Warmup")
	if !hasSuggestion && !hasWarmup {
		return KindNone, false
	}

	// Parse body once for structured checks.
	// Source: gateway_handler.go:1649-1663
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
		System []struct {
			Text string `json:"text"`
		} `json:"system"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return KindNone, false
	}

	// Shape 2: SUGGESTION MODE -- last user message text starts with "[SUGGESTION MODE:"
	// Source: gateway_handler.go:1665-1672
	if hasSuggestion && len(req.Messages) > 0 {
		last := req.Messages[len(req.Messages)-1]
		if last.Role == "user" && len(last.Content) > 0 &&
			last.Content[0].Type == "text" &&
			strings.HasPrefix(last.Content[0].Text, "[SUGGESTION MODE:") {
			return KindSuggestion, true
		}
	}

	// Shape 3: warmup / title-generation prompt
	// Source: gateway_handler.go:1674-1695
	if hasWarmup {
		for _, msg := range req.Messages {
			for _, c := range msg.Content {
				if c.Type == "text" {
					if strings.Contains(c.Text, "Please write a 5-10 word title for the following conversation:") ||
						c.Text == "Warmup" {
						return KindWarmup, true
					}
				}
			}
		}
		for _, sys := range req.System {
			if strings.Contains(sys.Text, "nalyze if this message indicates a new conversation topic. If it does, extract a 2-3 word title") {
				return KindWarmup, true
			}
		}
	}

	return KindNone, false
}

// IsClaudeCodeUserAgent returns true when the User-Agent header value indicates
// a Claude Code / claude-cli client.
// Source: service/gateway_service.go:3867-3873
func IsClaudeCodeUserAgent(userAgent string) bool {
	return strings.Contains(strings.ToLower(userAgent), "claude-cli/")
}

// generateRealisticMsgID generates a realistic-looking message ID.
// Source: gateway_handler.go:1800-1810
func generateRealisticMsgID() string {
	const charset = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	const idLen = 24
	randomBytes := make([]byte, idLen)
	if _, err := rand.Read(randomBytes); err != nil {
		return fmt.Sprintf("msg_bdrk_%d", time.Now().UnixNano())
	}
	b := make([]byte, idLen)
	for i := range b {
		b[i] = charset[int(randomBytes[i])%len(charset)]
	}
	return "msg_bdrk_" + string(b)
}

// SyntheticNonStreamBody returns the JSON body for a synthetic non-stream
// Anthropic Messages API response. status is always 200.
// Source: gateway_handler.go:1769-1810
func SyntheticNonStreamBody(kind Kind, model string) (status int, body []byte) {
	var msgID, text, stopReason string
	var outputTokens int

	switch kind {
	case KindSuggestion:
		msgID = "msg_mock_suggestion"
		text = ""
		outputTokens = 1
		stopReason = "end_turn"
	case KindConnProbe:
		msgID = generateRealisticMsgID()
		text = "#"
		outputTokens = 1
		stopReason = "max_tokens"
	default: // KindWarmup
		msgID = "msg_mock_warmup"
		text = "New Conversation"
		outputTokens = 2
		stopReason = "end_turn"
	}

	response := map[string]interface{}{
		"model": model,
		"id":    msgID,
		"type":  "message",
		"role":  "assistant",
		"content": []map[string]interface{}{
			{"type": "text", "text": text},
		},
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]interface{}{
			"input_tokens":                10,
			"cache_creation_input_tokens": 0,
			"cache_read_input_tokens":     0,
			"cache_creation": map[string]interface{}{
				"ephemeral_5m_input_tokens": 0,
				"ephemeral_1h_input_tokens": 0,
			},
			"output_tokens": outputTokens,
			"total_tokens":  10 + outputTokens,
		},
	}

	enc, err := json.Marshal(response)
	if err != nil {
		enc = []byte(`{"type":"message","role":"assistant","content":[],"stop_reason":"end_turn"}`)
	}
	return http.StatusOK, enc
}

// SyntheticStreamBody builds the SSE event stream bytes for a synthetic
// streaming Anthropic Messages API response.
// Source: gateway_handler.go:1699-1767
func SyntheticStreamBody(kind Kind, model string) []byte {
	var msgID string
	var outputTokens int
	var textDeltas []string

	switch kind {
	case KindSuggestion:
		msgID = "msg_mock_suggestion"
		outputTokens = 1
		textDeltas = []string{""}
	default: // KindWarmup (ConnProbe is always non-stream by definition)
		msgID = "msg_mock_warmup"
		outputTokens = 2
		textDeltas = []string{"New", " Conversation"}
	}

	messageStartJSON := `{"type":"message_start","message":{"id":` + strconv.Quote(msgID) +
		`,"type":"message","role":"assistant","model":` + strconv.Quote(model) +
		`,"content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":0}}}`

	events := []string{
		"event: message_start\ndata: " + messageStartJSON,
		`event: content_block_start` + "\n" + `data: {"content_block":{"text":"","type":"text"},"index":0,"type":"content_block_start"}`,
	}

	for _, text := range textDeltas {
		deltaJSON := `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":` +
			strconv.Quote(text) + `}}`
		events = append(events, "event: content_block_delta\ndata: "+deltaJSON)
	}

	messageDeltaJSON := `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":10,"output_tokens":` +
		strconv.Itoa(outputTokens) + `}}`

	events = append(events,
		`event: content_block_stop`+"\n"+`data: {"index":0,"type":"content_block_stop"}`,
		"event: message_delta\ndata: "+messageDeltaJSON,
		`event: message_stop`+"\n"+`data: {"type":"message_stop"}`,
	)

	var sb strings.Builder
	for _, ev := range events {
		sb.WriteString(ev)
		sb.WriteString("\n\n")
	}
	return []byte(sb.String())
}

// WriteNonStream writes the synthetic non-stream response to w.
func WriteNonStream(w http.ResponseWriter, kind Kind, model string) {
	status, body := SyntheticNonStreamBody(kind, model)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// WriteStream writes the synthetic SSE stream response to w.
func WriteStream(w http.ResponseWriter, kind Kind, model string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	body := SyntheticStreamBody(kind, model)
	_, _ = w.Write(body)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
