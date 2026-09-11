package provider

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestGrokOAuthChatUsesCLIProxyAndKeepsAPIKeyOnOfficialHost(t *testing.T) {
	t.Parallel()
	a := &OpenAICompatPassthroughAdapter{
		PlatformName: "grok",
		Endpoint:     "https://api.x.ai/v1/chat/completions",
	}

	keyReq, err := a.BuildRequest(context.Background(), BuildInput{
		InboundBody: []byte(`{"model":"grok-4.6","messages":[]}`),
		Credential:  Credential{Type: CredentialTypeAPIKey, Value: "xai-key"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if keyReq.URL.String() != "https://api.x.ai/v1/chat/completions" {
		t.Fatalf("API Key 必须打官方主机, got %s", keyReq.URL)
	}
	if keyReq.Header.Get("x-xai-token-auth") != "" {
		t.Fatal("API Key 不得带 CLI 身份头")
	}

	oauthReq, err := a.BuildRequest(context.Background(), BuildInput{
		InboundBody: []byte(`{"model":"grok-4.6","messages":[]}`),
		Credential:  Credential{Type: CredentialTypeOAuthAccessToken, Value: "sess-token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if oauthReq.URL.Host != GrokCLIChatProxyHost || oauthReq.URL.Path != "/v1/chat/completions" {
		t.Fatalf("OAuth 文本必须打 CLI 聊天代理, got %s", oauthReq.URL)
	}
	if oauthReq.Header.Get("x-xai-token-auth") != "xai-grok-cli" {
		t.Fatalf("缺少 CLI 身份头: %q", oauthReq.Header.Get("x-xai-token-auth"))
	}
	if !strings.Contains(oauthReq.Header.Get("User-Agent"), "HUAKAI-GrokCLI/") {
		t.Fatalf("User-Agent=%q", oauthReq.Header.Get("User-Agent"))
	}

	videoReq, err := a.BuildRequest(context.Background(), BuildInput{
		HTTPMethod:   http.MethodGet,
		EndpointPath: "/v1/videos/task-1",
		Credential:   Credential{Type: CredentialTypeOAuthAccessToken, Value: "sess-token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if videoReq.URL.Host != "api.x.ai" || videoReq.URL.Path != "/v1/videos/task-1" {
		t.Fatalf("媒体轮询必须留在官方 API, got %s", videoReq.URL)
	}
	if videoReq.Header.Get("x-xai-token-auth") != "" {
		t.Fatal("媒体出站不得带 CLI 身份头")
	}

	forcedOfficial, err := a.BuildRequest(context.Background(), BuildInput{
		InboundBody: []byte(`{"model":"grok-4.6"}`),
		Credential: Credential{
			Type:  CredentialTypeOAuthAccessToken,
			Value: "sess-token",
			Extra: map[string]string{"using_api": "true"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if forcedOfficial.URL.Host != "api.x.ai" {
		t.Fatalf("using_api 必须钉官方主机, got %s", forcedOfficial.URL)
	}
}
