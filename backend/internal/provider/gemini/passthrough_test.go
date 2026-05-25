// Gemini passthrough adapter 单元测试。
package gemini

import (
	"context"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestPassthroughAdapter_Platform(t *testing.T) {
	if got := (&PassthroughAdapter{}).Platform(); got != "gemini" {
		t.Errorf("Platform()=%q", got)
	}
}

func TestPassthroughAdapter_BuildRequest_HeaderAuth(t *testing.T) {
	a := &PassthroughAdapter{}
	in := provider.BuildInput{
		UpstreamModelID: "gemini-2.5-pro",
		InboundBody:     []byte(`{"contents":[]}`),
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "AIzaTestKey"},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.URL.String(), "/models/gemini-2.5-pro:generateContent") {
		t.Errorf("URL=%q 应含 gemini-2.5-pro:generateContent", req.URL.String())
	}
	if got := req.Header.Get("X-Goog-Api-Key"); got != "AIzaTestKey" {
		t.Errorf("X-Goog-Api-Key=%q", got)
	}
	// header 模式下 query 不应带 key
	if strings.Contains(req.URL.String(), "key=") {
		t.Errorf("URL 不应含 key= query 参数：%q", req.URL.String())
	}
}

func TestPassthroughAdapter_BuildRequest_QueryAuth(t *testing.T) {
	a := &PassthroughAdapter{}
	in := provider.BuildInput{
		UpstreamModelID: "gemini-2.5-flash",
		InboundBody:     []byte(`{"contents":[]}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "AIzaTestKey",
			Extra: map[string]string{"auth_in_query": "true"},
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.URL.String(), "key=AIzaTestKey") {
		t.Errorf("URL=%q 应含 key=AIzaTestKey", req.URL.String())
	}
	// query 模式下 header 不应有 X-Goog-Api-Key
	if got := req.Header.Get("X-Goog-Api-Key"); got != "" {
		t.Errorf("query 模式下 X-Goog-Api-Key 不应被设置，got=%q", got)
	}
}

func TestPassthroughAdapter_BuildRequest_StreamEndpoint(t *testing.T) {
	a := &PassthroughAdapter{}
	in := provider.BuildInput{
		UpstreamModelID: "gemini-2.5-pro",
		InboundBody:     []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "k",
			Extra: map[string]string{"stream": "true"},
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.URL.String(), ":streamGenerateContent") {
		t.Errorf("stream=true 应走 streamGenerateContent: URL=%q", req.URL.String())
	}
}

func TestPassthroughAdapter_BuildRequest_UserProject(t *testing.T) {
	a := &PassthroughAdapter{}
	in := provider.BuildInput{
		UpstreamModelID: "gemini-2.5-pro",
		InboundBody:     []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "k",
			Extra: map[string]string{"goog_user_project": "my-cloud-project"},
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("X-Goog-User-Project"); got != "my-cloud-project" {
		t.Errorf("X-Goog-User-Project=%q", got)
	}
}

func TestPassthroughAdapter_BuildRequest_CustomTemplate(t *testing.T) {
	a := &PassthroughAdapter{
		EndpointTemplate: "https://my-vertex-proxy.example/{model}:answer",
	}
	in := provider.BuildInput{
		UpstreamModelID: "custom-model",
		InboundBody:     []byte(`{}`),
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "k"},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.String() != "https://my-vertex-proxy.example/custom-model:answer" {
		t.Errorf("URL=%q", req.URL.String())
	}
}

func TestPassthroughAdapter_BuildRequest_RejectMissingModelID(t *testing.T) {
	a := &PassthroughAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		Credential: provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "k"},
	})
	if err == nil || !strings.Contains(err.Error(), "UpstreamModelID 不能为空") {
		t.Errorf("err=%v", err)
	}
}

func TestPassthroughAdapter_BuildRequest_RejectOAuth(t *testing.T) {
	a := &PassthroughAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "gemini-2.5-pro",
		Credential: provider.Credential{
			Type:  provider.CredentialTypeOAuthAccessToken,
			Value: "tok",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "不支持的凭据形态") {
		t.Errorf("err=%v", err)
	}
}

func TestPassthroughAdapter_BuildRequest_UpstreamPassthroughCustomHeader(t *testing.T) {
	a := &PassthroughAdapter{}
	in := provider.BuildInput{
		UpstreamModelID: "gemini-2.5-pro",
		InboundBody:     []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeUpstreamPassthrough,
			Value: "Bearer custom-token",
			Extra: map[string]string{"auth_header": "Authorization"},
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer custom-token" {
		t.Errorf("Authorization=%q", got)
	}
}
