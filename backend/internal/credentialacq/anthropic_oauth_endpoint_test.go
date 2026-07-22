package credentialacq

import (
	"net/url"
	"testing"
)

// TestClaudeAIOAuthTokenEndpointPinnedToPlatformHost 判别性锁死 AI-04 端点漂移修复:
// 官方 Claude Code 2.1.211 把换码端点从 api.anthropic.com 迁到 platform.claude.com。
// 断言解析后的 scheme/host/path 各字段,而非与生产常量自比——变异:把 claudeAIOAuthTokenURL
// 改回 https://api.anthropic.com/v1/oauth/token,本测试立即转红,挡住合并冲突/重构误回退。
func TestClaudeAIOAuthTokenEndpointPinnedToPlatformHost(t *testing.T) {
	parsed, err := url.Parse(claudeAIOAuthTokenURL)
	if err != nil {
		t.Fatalf("换码端点常量无法解析: %v", err)
	}
	if parsed.Scheme != "https" {
		t.Fatalf("换码端点 scheme=%q，必须 https", parsed.Scheme)
	}
	if parsed.Host != "platform.claude.com" {
		t.Fatalf("换码端点 host=%q，必须 platform.claude.com(官方 2.1.211);api.anthropic.com 仅承载推理转发", parsed.Host)
	}
	if parsed.Path != "/v1/oauth/token" {
		t.Fatalf("换码端点 path=%q，必须 /v1/oauth/token", parsed.Path)
	}
	// authorize 仍走 claude.ai,与 claudecookie 流程同一配对,不得随 token 端点漂移。
	authParsed, err := url.Parse(claudeAIOAuthAuthURL)
	if err != nil || authParsed.Host != "claude.ai" {
		t.Fatalf("authorize 端点 host=%q(err=%v)，必须 claude.ai", authParsed.Host, err)
	}
}
