// Mistral passthrough adapter 单元测试。
package mistral

import (
	"context"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestPassthroughAdapter_Platform(t *testing.T) {
	if got := (&PassthroughAdapter{}).Platform(); got != "mistral" {
		t.Errorf("Platform()=%q", got)
	}
}

func TestPassthroughAdapter_BuildRequest_APIKey(t *testing.T) {
	a := &PassthroughAdapter{}
	in := provider.BuildInput{
		InboundBody: []byte(`{"model":"mistral-large","messages":[]}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "ms-test-fake-key",
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.String() != "https://api.mistral.ai/v1/chat/completions" {
		t.Errorf("URL=%q", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer ms-test-fake-key" {
		t.Errorf("Authorization=%q", got)
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
	a := &PassthroughAdapter{Endpoint: "https://my-mistral-proxy.example/chat"}
	in := provider.BuildInput{
		InboundBody: []byte(`{}`),
		Credential:  provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "k"},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.String() != "https://my-mistral-proxy.example/chat" {
		t.Errorf("URL=%q", req.URL.String())
	}
}

func TestPassthroughAdapter_BuildRequest_UpstreamPassthrough(t *testing.T) {
	a := &PassthroughAdapter{}
	in := provider.BuildInput{
		InboundBody: []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeUpstreamPassthrough,
			Value: "Bearer custom-prefixed",
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer custom-prefixed" {
		t.Errorf("Authorization=%q", got)
	}
}
