package anthropic

import (
	"context"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func oauthInput(id int64) provider.BuildInput {
	return provider.BuildInput{
		UpstreamModelID: "claude-3-5-sonnet-20241022",
		InboundBody:     []byte(`{}`),
		Credential:      provider.Credential{Type: provider.CredentialTypeOAuthAccessToken, Value: "oauth-tok"},
		Account:         provider.AccountInfo{AccountID: id},
	}
}

// DEVPIN-02:OAuth/session 路径是池账号反封禁的主出口,必须带上 Claude Code
// 设备指纹,而不是以裸中转的形态发出去。
func TestOAuthSession_BuildRequest_DeviceProfile(t *testing.T) {
	a := &OAuthSessionAdapter{}
	req, err := a.BuildRequest(context.Background(), oauthInput(7))
	if err != nil {
		t.Fatal(err)
	}
	// MUTATION GUARD:去掉 applyClaudeDeviceProfile 会让这些头为空 -> OAuth
	// 出口变成裸中转(没有 Claude Code 签名)-> 变红。
	for _, h := range []string{"User-Agent", "X-Stainless-Package-Version", "X-Stainless-Os", "X-Stainless-Arch"} {
		if req.Header.Get(h) == "" {
			t.Fatalf("OAuth session egress missing %s (bare-relay tell)", h)
		}
	}
	if req.Header.Get("User-Agent") != claudeCodeUserAgent {
		t.Fatalf("User-Agent=%q want %q", req.Header.Get("User-Agent"), claudeCodeUserAgent)
	}
}

// OAuth 路径上同样要做到按账号区分(anti-clustering)。
func TestOAuthSession_BuildRequest_PerAccountDistinct(t *testing.T) {
	a := &OAuthSessionAdapter{}
	seen := map[string]bool{}
	for id := int64(1); id <= 64; id++ {
		req, err := a.BuildRequest(context.Background(), oauthInput(id))
		if err != nil {
			t.Fatal(err)
		}
		seen[req.Header.Get("X-Stainless-Os")+"/"+req.Header.Get("X-Stainless-Arch")] = true
	}
	if len(seen) < 2 {
		t.Fatalf("OAuth path device profile not per-account-distinct: %v", seen)
	}
}
