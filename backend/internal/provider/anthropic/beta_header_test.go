// DM-03 出站 Anthropic-Beta 合成测试。
package anthropic

import (
	"context"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// MUTATION: outboundBetaHeader 在无客户端 token 时重组凭据串(split+join)
// → verbatim 断言红;去掉 dedupe → merge 断言红;去掉 allow 过滤 → deny 红。
func TestOutboundBetaHeader_PureFn(t *testing.T) {
	// 无客户端 token → 凭据原值逐字节保留(含空格):既有流量出站零变化
	if got := outboundBetaHeader("a-1, b-2", nil, nil); got != "a-1, b-2" {
		t.Fatalf("verbatim: %q", got)
	}
	// 合并:凭据在前原样,客户端去重追加
	got := outboundBetaHeader("prompt-caching-2024-07-31",
		[]string{"prompt-caching-2024-07-31", "computer-use-2025-01-24"}, nil)
	if got != "prompt-caching-2024-07-31,computer-use-2025-01-24" {
		t.Fatalf("merge: %q", got)
	}
	// 凭据空 + 客户端有 → 仅客户端
	if got := outboundBetaHeader("", []string{"context-1m-2025-08-07"}, nil); got != "context-1m-2025-08-07" {
		t.Fatalf("client-only: %q", got)
	}
	// allow 全拒 → 凭据原值
	deny := func(string) bool { return false }
	if got := outboundBetaHeader("a-1", []string{"context-1m-2025-08-07"}, deny); got != "a-1" {
		t.Fatalf("deny: %q", got)
	}
}

// MUTATION: passthrough BuildRequest 忽略 in.InboundBetaTokens → 红。
// API-key 直连:未知 token(语法合法)宽放,凭据 token 原样在前,重复去重。
func TestPassthroughAdapter_MergesClientBetaTokens(t *testing.T) {
	a := &PassthroughAdapter{}
	req, err := a.BuildRequest(context.Background(), provider.BuildInput{
		InboundBody:       []byte(`{}`),
		InboundBetaTokens: []string{"computer-use-2024-10-22", "my-custom-beta-2026-01-01"},
		Credential: provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "sk-x",
			Extra: map[string]string{"anthropic_beta": "prompt-caching-2024-07-31,computer-use-2024-10-22"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Anthropic-Beta"); got != "prompt-caching-2024-07-31,computer-use-2024-10-22,my-custom-beta-2026-01-01" {
		t.Fatalf("Anthropic-Beta=%q (want 凭据在前原样+客户端去重追加+直连宽放)", got)
	}
}

// MUTATION: oauthBetaAllowed 放行任意 token(return true)→ 本测试红——
// 池账号指纹保护被打穿(真实 Claude Code 不会发的 beta 组合上行)。
func TestOAuthSessionAdapter_WhitelistsClientBetaTokens(t *testing.T) {
	adapter := &OAuthSessionAdapter{}
	req, err := adapter.BuildRequest(context.Background(), provider.BuildInput{
		InboundBody:       []byte(`{"model":"claude-3-5-sonnet-20241022","messages":[]}`),
		InboundBetaTokens: []string{"context-1m-2025-08-07", "totally-unknown-beta-9999"},
		Credential: provider.Credential{
			Type:  provider.CredentialTypeOAuthAccessToken,
			Value: "anthropic-oauth-access",
			Extra: map[string]string{
				"auth_mode":      credentialstore.AuthModeClaudeAIOAuth,
				"anthropic_beta": "oauth-2025-04-20",
			},
		},
	})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if got := req.Header.Get("Anthropic-Beta"); got != "oauth-2025-04-20,context-1m-2025-08-07" {
		t.Fatalf("Anthropic-Beta=%q (want 白名单放行 context-1m,未知 token 丢弃)", got)
	}
}
