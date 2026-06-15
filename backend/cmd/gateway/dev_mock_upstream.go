package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
)

// DevMockUpstreamEnv, when truthy AND the gateway is NOT in production mode,
// injects a fake upstream HTTP doer so the full request loop (dispatch → forward
// → bill → usage) runs locally with NO real provider credential or network. It
// exists for local MVP demos and the smoke test; it is hard-gated off in
// production so a real deployment can never silently fabricate upstream traffic.
const DevMockUpstreamEnv = "HUAKAI_DEV_MOCK_UPSTREAM"

// devMockUpstreamDoer returns the fake doer when enabled, else nil. A nil doer
// leaves UpstreamDispatcher on its real transport/proxy/TLS path untouched.
func devMockUpstreamDoer() gateway.HTTPDoer {
	if releaseModeProduction() {
		return nil
	}
	if on, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv(DevMockUpstreamEnv))); !on {
		return nil
	}
	return mockUpstreamDoer{}
}

type mockUpstreamDoer struct{}

// Do fabricates a deterministic provider SSE stream in the protocol shape the
// outbound request targets (Anthropic Messages vs OpenAI Chat), so the matching
// vendor adapter in the forwarder parses a non-zero usage draft and billing
// commits exactly as it would against a real provider.
func (mockUpstreamDoer) Do(req *http.Request) (*http.Response, error) {
	const inTok, outTok = 12, 8
	var body []byte
	if req != nil && req.URL != nil && strings.Contains(req.URL.Path, "/messages") {
		body = gatewayhttp.MockAnthropicUpstreamBytes("msg_devmock", "dev-mock-model", inTok, outTok)
	} else {
		body = mockOpenAIChatSSE(inTok, outTok)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    req,
	}, nil
}

// mockOpenAIChatSSE emits a 2-chunk + [DONE] OpenAI chat.completion.chunk stream
// whose final chunk carries a usage block, mirroring a real streaming response.
func mockOpenAIChatSSE(inTok, outTok int) []byte {
	var b bytes.Buffer
	delta := map[string]any{
		"id":      "chatcmpl-devmock",
		"object":  "chat.completion.chunk",
		"model":   "dev-mock-model",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": "ok"}}},
	}
	raw, _ := json.Marshal(delta)
	fmt.Fprintf(&b, "data: %s\n\n", raw)
	final := map[string]any{
		"id":      "chatcmpl-devmock",
		"object":  "chat.completion.chunk",
		"model":   "dev-mock-model",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": inTok, "completion_tokens": outTok, "total_tokens": inTok + outTok},
	}
	rawF, _ := json.Marshal(final)
	fmt.Fprintf(&b, "data: %s\n\n", rawF)
	fmt.Fprint(&b, "data: [DONE]\n\n")
	return b.Bytes()
}
