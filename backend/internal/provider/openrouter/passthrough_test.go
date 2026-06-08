// OpenRouter passthrough adapter 单元测试。
package openrouter

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestPassthroughAdapter_Platform(t *testing.T) {
	if got := (&PassthroughAdapter{}).Platform(); got != "openrouter" {
		t.Errorf("Platform()=%q", got)
	}
}

func TestPassthroughAdapter_BuildRequest_APIKey(t *testing.T) {
	a := &PassthroughAdapter{}
	in := provider.BuildInput{
		InboundBody: []byte(`{"model":"openai/gpt-4o","messages":[]}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "sk-or-test",
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.String() != "https://openrouter.ai/api/v1/chat/completions" {
		t.Errorf("URL=%q", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer sk-or-test" {
		t.Errorf("Authorization=%q", got)
	}
}

func TestPassthroughAdapter_BuildRequest_AttributionHeaders(t *testing.T) {
	a := &PassthroughAdapter{}
	in := provider.BuildInput{
		InboundBody: []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "sk-or",
			Extra: map[string]string{
				"http_referer": "https://huakai.example.com",
				"x_title":      "HUAKAI Gateway",
			},
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("HTTP-Referer"); got != "https://huakai.example.com" {
		t.Errorf("HTTP-Referer=%q", got)
	}
	if got := req.Header.Get("X-Title"); got != "HUAKAI Gateway" {
		t.Errorf("X-Title=%q", got)
	}
}

func TestPassthroughAdapter_BuildRequest_ZDRExtraInjectsProviderPreference(t *testing.T) {
	a := &PassthroughAdapter{}
	in := provider.BuildInput{
		InboundBody: []byte(`{"model":"openai/gpt-4o","messages":[]}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "sk-or",
			Extra: map[string]string{"openrouter_zdr": "true"},
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("request body is not JSON: %v; body=%s", err, string(body))
	}
	providerPrefs, ok := out["provider"].(map[string]any)
	if !ok {
		t.Fatalf("provider preferences missing from body: %s", string(body))
	}
	if got, ok := providerPrefs["zdr"].(bool); !ok || !got {
		t.Fatalf("provider.zdr=%v (ok=%t), want true", providerPrefs["zdr"], ok)
	}
}

func TestPassthroughAdapter_BuildRequest_ZDRAbsentLeavesBodyUnchanged(t *testing.T) {
	a := &PassthroughAdapter{}
	body := []byte(`{"model":"openai/gpt-4o","messages":[]}`)
	in := provider.BuildInput{
		InboundBody: body,
		Credential: provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "sk-or",
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("body changed without openrouter_zdr extra: got %s want %s", string(got), string(body))
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

func TestPassthroughAdapter_BuildRequest_RejectOAuth(t *testing.T) {
	a := &PassthroughAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		Credential: provider.Credential{Type: provider.CredentialTypeOAuthAccessToken, Value: "tok"},
	})
	if err == nil || !strings.Contains(err.Error(), "不支持的凭据形态") {
		t.Errorf("err=%v", err)
	}
}

func TestPassthroughAdapter_BuildRequest_CustomEndpoint(t *testing.T) {
	a := &PassthroughAdapter{Endpoint: "https://my-or-proxy.example/chat"}
	in := provider.BuildInput{
		InboundBody: []byte(`{}`),
		Credential:  provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "k"},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.String() != "https://my-or-proxy.example/chat" {
		t.Errorf("URL=%q", req.URL.String())
	}
}
