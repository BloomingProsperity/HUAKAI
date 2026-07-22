package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/devupstream"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

// DevMockUpstreamEnv 为真值且网关不在 production 模式时,注入一个假的上游 HTTP doer,
// 让完整请求回路(dispatch → forward → bill → usage)在本地无需任何真实 provider 凭据
// 或网络即可跑通。它仅供本地 MVP 演示和 smoke 测试使用;在 production 下被硬性禁用,
// 确保真实部署绝不会静默伪造上游流量。
const DevMockUpstreamEnv = "HUAKAI_DEV_MOCK_UPSTREAM"

// DevMockUpstreamDelayMSEnv 仅供测试把 dev mock 上游请求保持一小段时间,制造真实
// HTTP 请求重叠。未设置或为 0 时完全保持原有瞬时返回行为。
const DevMockUpstreamDelayMSEnv = "HUAKAI_DEV_MOCK_UPSTREAM_DELAY_MS"

// devMockUpstreamDoer 在启用时返回假 doer,否则返回 nil。返回 nil 时
// UpstreamDispatcher 保持在其真实的 transport/proxy/TLS 路径上,不受影响。
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

// Do 按出站请求所针对的协议形态(Anthropic Messages 还是 OpenAI Chat)伪造一段
// 确定性的 provider SSE 流,使 forwarder 中匹配的厂商适配器解析出非零的 usage 草稿,
// 计费提交的行为与对接真实 provider 时完全一致。
func (mockUpstreamDoer) Do(req *http.Request) (*http.Response, error) {
	const inTok, outTok = 12, 8
	if delay := devMockUpstreamDelay(); delay > 0 {
		ctx := contextForMockRequest(req)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	var body []byte
	if req != nil && req.URL != nil {
		if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/models") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(bytes.NewReader(mockModelCatalog())),
				Request:    req,
			}, nil
		}
		switch {
		case strings.Contains(req.URL.Path, "/messages"):
			body = devupstream.MockAnthropicUpstreamBytes("msg_devmock", "dev-mock-model", inTok, outTok)
		case strings.Contains(req.URL.Path, "streamGenerateContent"):
			body = mockGeminiSSE(inTok, outTok)
		}
	}
	if len(body) == 0 {
		body = mockOpenAIChatSSE(inTok, outTok)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    req,
	}, nil
}

func contextForMockRequest(req *http.Request) context.Context {
	if req == nil {
		return context.Background()
	}
	return req.Context()
}

func devMockUpstreamDelay() time.Duration {
	raw := strings.TrimSpace(os.Getenv(DevMockUpstreamDelayMSEnv))
	if raw == "" {
		return 0
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

// mockModelCatalog 按 OpenAI 兼容形状返回确定性的上游模型目录,供账号级模型
// 发现/同步链在无真实上游时端到端跑通。目录 ID 刻意不等于任何公开别名,以便
// 冒烟测试能证明同步落库值与选号用的 ProviderModelID 完全一致。
func mockModelCatalog() []byte {
	return []byte(`{"object":"list","data":[{"id":"dev-mock-model"},{"id":"dev-mock-model-secondary"}]}`)
}

// mockOpenAIChatSSE 输出一段「2 个 chunk + [DONE]」的 OpenAI chat.completion.chunk 流,
// 其最后一个 chunk 携带 usage 块,模拟真实的流式响应。
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

func mockGeminiSSE(inTok, outTok int) []byte {
	payload := map[string]any{
		"candidates": []any{map[string]any{
			"index": 0, "finishReason": "STOP",
			"content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": "ok"}}},
		}},
		"usageMetadata": map[string]any{
			"promptTokenCount": inTok, "candidatesTokenCount": outTok, "totalTokenCount": inTok + outTok,
		},
	}
	raw, _ := json.Marshal(payload)
	return append(append([]byte("data: "), raw...), []byte("\n\n")...)
}
