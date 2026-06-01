// Anthropic passthrough adapter 单元测试。
package anthropic

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestPassthroughAdapter_Platform(t *testing.T) {
	if got := (&PassthroughAdapter{}).Platform(); got != "anthropic" {
		t.Errorf("Platform()=%q want anthropic", got)
	}
}

func TestPassthroughAdapter_BuildRequest_APIKey(t *testing.T) {
	a := &PassthroughAdapter{}
	in := provider.BuildInput{
		UpstreamModelID: "claude-3-5-sonnet-20241022",
		InboundBody:     []byte(`{"model":"claude-3-5-sonnet-20241022","max_tokens":1024,"messages":[]}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "sk-ant-api03-test-fake",
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != "POST" {
		t.Errorf("Method=%q", req.Method)
	}
	if req.URL.String() != "https://api.anthropic.com/v1/messages" {
		t.Errorf("URL=%q", req.URL.String())
	}
	// Anthropic 用 X-API-Key，不是 Bearer
	if got := req.Header.Get("X-API-Key"); got != "sk-ant-api03-test-fake" {
		t.Errorf("X-API-Key=%q", got)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization 不应被设置，got=%q", got)
	}
	// anthropic-version 默认必填
	if got := req.Header.Get("Anthropic-Version"); got != "2023-06-01" {
		t.Errorf("Anthropic-Version=%q", got)
	}
	body, _ := io.ReadAll(req.Body)
	if !strings.Contains(string(body), "claude-3-5-sonnet") {
		t.Errorf("body 未透传：%s", body)
	}
}

func TestPassthroughAdapter_BuildRequest_CustomVersionAndBetas(t *testing.T) {
	a := &PassthroughAdapter{}
	in := provider.BuildInput{
		InboundBody: []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "sk-x",
			Extra: map[string]string{
				"anthropic_version": "2024-10-22",
				"anthropic_beta":    "prompt-caching-2024-07-31,computer-use-2024-10-22",
			},
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Anthropic-Version"); got != "2024-10-22" {
		t.Errorf("Anthropic-Version=%q", got)
	}
	if got := req.Header.Get("Anthropic-Beta"); got != "prompt-caching-2024-07-31,computer-use-2024-10-22" {
		t.Errorf("Anthropic-Beta=%q", got)
	}
}

func TestPassthroughAdapter_BuildRequest_CustomEndpoint(t *testing.T) {
	a := &PassthroughAdapter{Endpoint: "https://custom.proxy.example/v1/messages"}
	in := provider.BuildInput{
		InboundBody: []byte(`{}`),
		Credential:  provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-x"},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.String() != "https://custom.proxy.example/v1/messages" {
		t.Errorf("URL=%q", req.URL.String())
	}
}

func TestPassthroughAdapter_BuildRequest_UpstreamPassthroughCustomHeader(t *testing.T) {
	a := &PassthroughAdapter{}
	in := provider.BuildInput{
		InboundBody: []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeUpstreamPassthrough,
			Value: "Bearer token-from-proxy",
			Extra: map[string]string{"auth_header": "Authorization"},
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer token-from-proxy" {
		t.Errorf("Authorization=%q", got)
	}
	if got := req.Header.Get("X-API-Key"); got != "" {
		t.Errorf("X-API-Key 不应被设置，got=%q", got)
	}
}

func TestPassthroughAdapter_BuildRequest_RejectsUnsafeUpstreamBaseURLBeforeSecret(t *testing.T) {
	a := &PassthroughAdapter{}
	req, err := a.BuildRequest(context.Background(), provider.BuildInput{
		InboundBody: []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeUpstreamPassthrough,
			Value: "Bearer secret-token",
			Extra: map[string]string{
				"auth_header": "Authorization",
				"base_url":    "http://127.0.0.1:8080/v1/messages",
			},
		},
	})
	if err == nil {
		if req != nil && req.Header.Get("Authorization") != "" {
			t.Fatalf("unsafe base_url built request with secret Authorization header: url=%s", req.URL.String())
		}
		t.Fatal("unsafe base_url should be rejected before request construction")
	}
	if strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("unsafe base_url rejection leaked destination or secret: %v", err)
	}
}

func TestPassthroughAdapter_BuildRequest_RejectOAuthCredential(t *testing.T) {
	// Pro/Max OAuth 反转走另外的 adapter（暂停）；passthrough 不接受
	a := &PassthroughAdapter{}
	in := provider.BuildInput{
		InboundBody: []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeOAuthAccessToken,
			Value: "oauth-tok",
		},
	}
	_, err := a.BuildRequest(context.Background(), in)
	if err == nil {
		t.Fatal("OAuth 凭据应被 reject")
	}
	if !strings.Contains(err.Error(), "不支持的凭据形态") {
		t.Errorf("error=%v", err)
	}
}

func TestPassthroughAdapter_BuildRequest_RejectEmpty(t *testing.T) {
	a := &PassthroughAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		Credential: provider.Credential{Type: provider.CredentialTypeAPIKey, Value: ""},
	})
	if err == nil || !strings.Contains(err.Error(), "凭据 Value 为空") {
		t.Errorf("err=%v", err)
	}
}

func TestPassthroughAdapter_AcceptableCredentialTypes(t *testing.T) {
	got := (&PassthroughAdapter{}).AcceptableCredentialTypes()
	if len(got) != 2 {
		t.Fatalf("AcceptableCredentialTypes 长度=%d want 2", len(got))
	}
}
