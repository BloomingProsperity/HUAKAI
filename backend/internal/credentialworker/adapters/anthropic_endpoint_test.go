package adapters

import (
	"net/url"
	"testing"
)

// TestAnthropicRefreshEndpointPinnedToPlatformHost 判别性锁死 AI-04 刷新端点漂移修复:
// 断言解析后的 scheme/host/path,而非与生产常量自比——变异:把 defaultAnthropicTokenEndpoint
// 改回 https://api.anthropic.com/v1/oauth/token,本测试立即转红。
func TestAnthropicRefreshEndpointPinnedToPlatformHost(t *testing.T) {
	parsed, err := url.Parse(defaultAnthropicTokenEndpoint)
	if err != nil {
		t.Fatalf("刷新端点常量无法解析: %v", err)
	}
	if parsed.Scheme != "https" {
		t.Fatalf("刷新端点 scheme=%q，必须 https", parsed.Scheme)
	}
	if parsed.Host != "platform.claude.com" {
		t.Fatalf("刷新端点 host=%q，必须 platform.claude.com(官方 2.1.211)", parsed.Host)
	}
	if parsed.Path != "/v1/oauth/token" {
		t.Fatalf("刷新端点 path=%q，必须 /v1/oauth/token", parsed.Path)
	}
}
