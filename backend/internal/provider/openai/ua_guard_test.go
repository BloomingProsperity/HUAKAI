package openai

import (
	"context"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestIsBrowserUserAgent(t *testing.T) {
	browser := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120 Safari/537.36",
		"mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)",
		"  Mozilla/5.0 leading-space",
		"Opera/9.80 (Windows NT 6.0)",
	}
	for _, ua := range browser {
		if !isBrowserUserAgent(ua) {
			t.Errorf("isBrowserUserAgent(%q)=false, want true", ua)
		}
	}
	notBrowser := []string{"", "codex/1.0.0 (linux; go)", "OpenAI-Codex/2.0", "python-requests/2.31"}
	for _, ua := range notBrowser {
		if isBrowserUserAgent(ua) {
			t.Errorf("isBrowserUserAgent(%q)=true, want false", ua)
		}
	}
}

// 变异哨兵：若移除 BuildRequest 中的 isBrowserUserAgent 改写，浏览器型 UA 会
// 泄漏到 OpenAI/Codex 上游 —— 此断言会变红（届时上游 WAF 会把我们指纹识别为
// 非官方客户端）。
func TestCodexSession_BrowserUAScrubbed(t *testing.T) {
	a := &CodexSessionAdapter{}
	in := provider.BuildInput{
		UpstreamModelID: "gpt-4o",
		InboundBody:     []byte("{}"),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "sess-tok",
			Extra: map[string]string{"user_agent": "Mozilla/5.0 (Windows NT 10.0) Chrome/120"},
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("User-Agent"); got != defaultCodexUserAgent {
		t.Fatalf("browser UA leaked: User-Agent=%q, want scrubbed to %q", got, defaultCodexUserAgent)
	}
}

// 合法的非浏览器（CLI 风格）UA 应原样透传不被改写。
func TestCodexSession_LegitUAPreserved(t *testing.T) {
	a := &CodexSessionAdapter{}
	const legit = "codex_cli_rs/0.5.0 (Linux x86_64)"
	in := provider.BuildInput{
		UpstreamModelID: "gpt-4o",
		InboundBody:     []byte("{}"),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "sess-tok",
			Extra: map[string]string{"user_agent": legit},
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("User-Agent"); got != legit {
		t.Fatalf("legit UA wrongly altered: got %q want %q", got, legit)
	}
}
