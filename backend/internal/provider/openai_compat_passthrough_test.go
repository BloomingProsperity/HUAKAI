package provider

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestOpenAICompatPassthroughAdapter_FoldedPlatforms(t *testing.T) {
	cases := []struct {
		platform string
		endpoint string
		apiKey   string
		body     []byte
	}{
		{
			platform: "deepseek",
			endpoint: "https://api.deepseek.com/v1/chat/completions",
			apiKey:   "ds-test-fake-key",
			body:     []byte(`{"model":"deepseek-chat","messages":[]}`),
		},
		{
			platform: "fireworks",
			endpoint: "https://api.fireworks.ai/inference/v1/chat/completions",
			apiKey:   "fw-test-fake-key",
			body:     []byte(`{"model":"accounts/fireworks/models/test","messages":[]}`),
		},
		{
			platform: "grok",
			endpoint: "https://api.x.ai/v1/chat/completions",
			apiKey:   "xai-test-fake-key",
			body:     []byte(`{"model":"grok-3","messages":[]}`),
		},
		{
			platform: "groqcloud",
			endpoint: "https://api.groq.com/openai/v1/chat/completions",
			apiKey:   "gsk-test-fake-key",
			body:     []byte(`{"model":"llama-3.3-70b-versatile","messages":[]}`),
		},
		{
			platform: "mistral",
			endpoint: "https://api.mistral.ai/v1/chat/completions",
			apiKey:   "mk-test-fake-key",
			body:     []byte(`{"model":"mistral-large-latest","messages":[]}`),
		},
		{
			platform: "perplexity",
			endpoint: "https://api.perplexity.ai/chat/completions",
			apiKey:   "pplx-test-fake-key",
			body:     []byte(`{"model":"sonar-pro","messages":[]}`),
		},
		{
			platform: "together",
			endpoint: "https://api.together.xyz/v1/chat/completions",
			apiKey:   "tg-test-fake-key",
			body:     []byte(`{"model":"meta-llama/Llama-3.3-70B-Instruct-Turbo","messages":[]}`),
		},
	}

	for _, tc := range cases {
		t.Run(tc.platform, func(t *testing.T) {
			a := &OpenAICompatPassthroughAdapter{
				PlatformName: tc.platform,
				Endpoint:     tc.endpoint,
			}
			if got := a.Platform(); got != tc.platform {
				t.Fatalf("Platform()=%q want %q", got, tc.platform)
			}

			req, err := a.BuildRequest(context.Background(), BuildInput{
				InboundBody: tc.body,
				Credential: Credential{
					Type:  CredentialTypeAPIKey,
					Value: tc.apiKey,
				},
			})
			if err != nil {
				t.Fatalf("BuildRequest API key: %v", err)
			}
			if req.URL.String() != tc.endpoint {
				t.Errorf("URL=%q want %q", req.URL.String(), tc.endpoint)
			}
			if got := req.Header.Get("Authorization"); got != "Bearer "+tc.apiKey {
				t.Errorf("Authorization=%q", got)
			}
			if got := req.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type=%q", got)
			}
			if got := req.Header.Get("Accept"); got != "application/json" {
				t.Errorf("Accept=%q", got)
			}

			_, err = a.BuildRequest(context.Background(), BuildInput{
				Credential: Credential{Type: CredentialTypeAPIKey, Value: ""},
			})
			if err == nil || !strings.Contains(err.Error(), tc.platform+" passthrough: 凭据 Value 为空") {
				t.Fatalf("missing credential err=%v", err)
			}
		})
	}
}

func TestOpenAICompatPassthroughAdapter_CredentialAuthorization(t *testing.T) {
	a := &OpenAICompatPassthroughAdapter{
		PlatformName: "grok",
		Endpoint:     "https://api.x.ai/v1/chat/completions",
	}

	// 变异：从接受列表移除 OAuth access token 会让 oauth_access_token 子用例返回
	// “不支持的凭据形态”；只移除请求头分支则会得到空 Authorization，两者都会转红。
	credentialCases := []struct {
		name              string
		credential        Credential
		wantAuthorization string
	}{
		{
			name:              "api_key 使用 Bearer",
			credential:        Credential{Type: CredentialTypeAPIKey, Value: "api-key-value"},
			wantAuthorization: "Bearer api-key-value",
		},
		{
			name:              "OAuth access token 使用 Bearer",
			credential:        Credential{Type: CredentialTypeOAuthAccessToken, Value: "oauth-token-value"},
			wantAuthorization: "Bearer oauth-token-value",
		},
		{
			name:              "upstream passthrough 保持原值",
			credential:        Credential{Type: CredentialTypeUpstreamPassthrough, Value: "Custom upstream-value"},
			wantAuthorization: "Custom upstream-value",
		},
	}
	for _, tc := range credentialCases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := a.BuildRequest(context.Background(), BuildInput{
				InboundBody: []byte(`{}`),
				Credential:  tc.credential,
			})
			if err != nil {
				t.Fatalf("BuildRequest: %v", err)
			}
			if got := req.Header.Get("Authorization"); got != tc.wantAuthorization {
				t.Fatalf("Authorization=%q want %q", got, tc.wantAuthorization)
			}
		})
	}

	gotTypes := a.AcceptableCredentialTypes()
	wantTypes := []CredentialType{
		CredentialTypeAPIKey,
		CredentialTypeOAuthAccessToken,
		CredentialTypeUpstreamPassthrough,
	}
	if len(gotTypes) != len(wantTypes) {
		t.Fatalf("AcceptableCredentialTypes length=%d want %d: %v", len(gotTypes), len(wantTypes), gotTypes)
	}
	for i := range wantTypes {
		if gotTypes[i] != wantTypes[i] {
			t.Fatalf("AcceptableCredentialTypes[%d]=%q want %q", i, gotTypes[i], wantTypes[i])
		}
	}

	customEndpoint := "https://my-deepseek-proxy.example/chat"
	req, err := (&OpenAICompatPassthroughAdapter{
		PlatformName: "deepseek",
		Endpoint:     customEndpoint,
	}).BuildRequest(context.Background(), BuildInput{
		InboundBody: []byte(`{}`),
		Credential:  Credential{Type: CredentialTypeAPIKey, Value: "k"},
	})
	if err != nil {
		t.Fatalf("custom endpoint BuildRequest: %v", err)
	}
	if req.URL.String() != customEndpoint {
		t.Fatalf("custom endpoint URL=%q want %q", req.URL.String(), customEndpoint)
	}
}

func TestOpenAICompatPassthroughAdapter_RejectsUnsafeUpstreamBaseURL(t *testing.T) {
	a := &OpenAICompatPassthroughAdapter{
		PlatformName: "deepseek",
		Endpoint:     "https://api.deepseek.com/v1/chat/completions",
	}

	_, err := a.BuildRequest(context.Background(), BuildInput{
		Credential: Credential{
			Type:  CredentialTypeUpstreamPassthrough,
			Value: "Bearer custom-prefixed",
			Extra: map[string]string{
				"base_url": "http://127.0.0.1:8080",
			},
		},
	})
	if !errors.Is(err, ErrUnsafePassthroughEndpoint) {
		t.Fatalf("err=%v want ErrUnsafePassthroughEndpoint", err)
	}
	if err == nil || !strings.Contains(err.Error(), "deepseek passthrough: endpoint rejected") {
		t.Fatalf("unsafe endpoint err=%v", err)
	}
}

func TestOpenAICompatPassthroughAdapter_GetEndpointOverride(t *testing.T) {
	adapter := &OpenAICompatPassthroughAdapter{
		PlatformName: "grok",
		Endpoint:     "https://api.x.ai/v1/chat/completions",
	}
	request, err := adapter.BuildRequest(context.Background(), BuildInput{
		HTTPMethod: http.MethodGet, EndpointPath: "/v1/videos/request-1",
		InboundBody: []byte(`{"must_not_be_sent":true}`),
		Credential: Credential{Type: CredentialTypeAPIKey, Value: "xai-test"},
	})
	if err != nil {
		t.Fatalf("BuildRequest GET: %v", err)
	}
	if request.Method != http.MethodGet || request.URL.String() != "https://api.x.ai/v1/videos/request-1" {
		t.Fatalf("method/url=%s %s", request.Method, request.URL)
	}
	if request.Header.Get("Content-Type") != "" || request.Header.Get("Authorization") != "Bearer xai-test" {
		t.Fatalf("headers=%v", request.Header)
	}
	if _, err := adapter.BuildRequest(context.Background(), BuildInput{
		HTTPMethod: http.MethodDelete,
		Credential: Credential{Type: CredentialTypeAPIKey, Value: "xai-test"},
	}); err == nil {
		t.Fatal("unsupported method must fail closed")
	}
}
