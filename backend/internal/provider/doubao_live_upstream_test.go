//go:build live_upstream

// 豆包(火山方舟 Ark)真上游连通测试。用 HUAKAI 真的 registrydefault 豆包 adapter
// 构造出站请求(端点解析 + Bearer 鉴权注入全走生产码),打到活的 ark 上游,断言
// HUAKAI 的接线在真上游上能拿到有效回复。
//
// 只在带 -tags=live_upstream 且设了 ARK_KEY 环境变量时运行;普通 CI 不触发(不烧钱、
// 不依赖外网)。跑法:
//   ARK_KEY=ark-xxx go test -tags=live_upstream -run TestDoubaoLiveUpstream \
//     ./internal/provider/ -count=1 -v
package provider_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
)

// TestDoubaoLiveUpstream 证明 HUAKAI 的 doubao_chat adapter 能把一个 OpenAI 形态
// 请求正确送达火山方舟并取回回复。判别性:若 adapter 端点解析或鉴权注入错了(如
// 打到默认 openai.com、或漏了 Bearer),上游会 401/404,断言 200+content 立刻红。
func TestDoubaoLiveUpstream(t *testing.T) {
	arkKey := os.Getenv("ARK_KEY")
	if arkKey == "" {
		t.Skip("未设 ARK_KEY,跳过真上游测试")
	}

	// 用生产 registry 取豆包 adapter——不手搓,端点即 registrydefault 里的 ark 默认值。
	adapter, err := registrydefault.Build().For("doubao_chat")
	if err != nil {
		t.Fatalf("取 doubao_chat adapter 失败: %v", err)
	}

	// 最便宜的 mini + thinking 关闭(省钱,mini 是 reasoning 模型不关会烧思考 token)
	// + 极小 max_tokens。model 名必须是 ark 的精确 model id(passthrough 原样透传 body)。
	body, err := json.Marshal(map[string]any{
		"model":      "doubao-seed-2-0-mini-260428",
		"messages":   []map[string]string{{"role": "user", "content": "只回两个字:你好"}},
		"max_tokens": 16,
		"thinking":   map[string]string{"type": "disabled"},
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	req, err := adapter.BuildRequest(ctx, provider.BuildInput{
		UpstreamModelID: "doubao-seed-2-0-mini-260428",
		InboundBody:     body,
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: arkKey},
	})
	if err != nil {
		t.Fatalf("BuildRequest 失败: %v", err)
	}

	// 核实 HUAKAI 真的把请求指向了 ark(而非某默认 openai 端点)——接线正确性断言。
	if req.URL.Host != "ark.cn-beijing.volces.com" {
		t.Fatalf("adapter 出站 host 错了,得 ark.cn-beijing.volces.com,实得 %q", req.URL.Host)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("发往 ark 失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ark 返回非 200: %d, body=%s", resp.StatusCode, truncate(raw, 400))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage map[string]any `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("解析 ark 响应失败: %v, body=%s", err, truncate(raw, 400))
	}
	if len(parsed.Choices) == 0 || parsed.Choices[0].Message.Content == "" {
		t.Fatalf("ark 回复无有效内容: %s", truncate(raw, 400))
	}
	t.Logf("HUAKAI→豆包 通:content=%q usage=%v", parsed.Choices[0].Message.Content, parsed.Usage)
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}
