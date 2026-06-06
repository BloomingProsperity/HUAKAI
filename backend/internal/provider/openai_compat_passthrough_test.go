package provider

import (
	"context"
	"errors"
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

func TestOpenAICompatPassthroughAdapter_LegacyCredentialPaths(t *testing.T) {
	a := &OpenAICompatPassthroughAdapter{
		PlatformName: "deepseek",
		Endpoint:     "https://api.deepseek.com/v1/chat/completions",
	}

	gotTypes := a.AcceptableCredentialTypes()
	wantTypes := []CredentialType{CredentialTypeAPIKey, CredentialTypeUpstreamPassthrough}
	if len(gotTypes) != len(wantTypes) {
		t.Fatalf("AcceptableCredentialTypes length=%d want %d: %v", len(gotTypes), len(wantTypes), gotTypes)
	}
	for i := range wantTypes {
		if gotTypes[i] != wantTypes[i] {
			t.Fatalf("AcceptableCredentialTypes[%d]=%q want %q", i, gotTypes[i], wantTypes[i])
		}
	}

	_, err := a.BuildRequest(context.Background(), BuildInput{
		Credential: Credential{Type: CredentialTypeOAuthAccessToken, Value: "tok"},
	})
	if err == nil || !strings.Contains(err.Error(), "deepseek passthrough: 不支持的凭据形态") {
		t.Fatalf("oauth rejection err=%v", err)
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

	req, err = a.BuildRequest(context.Background(), BuildInput{
		InboundBody: []byte(`{}`),
		Credential: Credential{
			Type:  CredentialTypeUpstreamPassthrough,
			Value: "Bearer custom-prefixed",
		},
	})
	if err != nil {
		t.Fatalf("upstream passthrough BuildRequest: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer custom-prefixed" {
		t.Fatalf("upstream passthrough Authorization=%q", got)
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
