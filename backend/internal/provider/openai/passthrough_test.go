// OpenAI passthrough 适配器 — 表格驱动测试。
package openai

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestPassthroughAdapter_Platform(t *testing.T) {
	a := &PassthroughAdapter{}
	if got := a.Platform(); got != "openai" {
		t.Errorf("Platform()=%q want openai", got)
	}
}

func TestPassthroughAdapter_AcceptableCredentialTypes(t *testing.T) {
	a := &PassthroughAdapter{}
	got := a.AcceptableCredentialTypes()
	if len(got) != 2 {
		t.Fatalf("AcceptableCredentialTypes 长度=%d want 2: %v", len(got), got)
	}
	want := map[provider.CredentialType]bool{
		provider.CredentialTypeAPIKey:              true,
		provider.CredentialTypeUpstreamPassthrough: true,
	}
	for _, ct := range got {
		if !want[ct] {
			t.Errorf("意外的凭据类型 %q", ct)
		}
	}
}

func TestPassthroughAdapter_BuildRequest_APIKey(t *testing.T) {
	a := &PassthroughAdapter{}
	in := provider.BuildInput{
		UpstreamModelID: "gpt-4o",
		InboundBody:     []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "sk-test-fake-key-not-real",
		},
		Account: provider.AccountInfo{AccountID: 42, Platform: "openai", AccountType: "apikey"},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != "POST" {
		t.Errorf("Method=%q want POST", req.Method)
	}
	if req.URL.String() != "https://api.openai.com/v1/chat/completions" {
		t.Errorf("URL=%q want OpenAI 官方 endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer sk-test-fake-key-not-real" {
		t.Errorf("Authorization=%q", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type=%q", got)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept=%q", got)
	}
	body, _ := io.ReadAll(req.Body)
	if !strings.Contains(string(body), `"model":"gpt-4o"`) {
		t.Errorf("body 未透传：%s", body)
	}
}

func TestPassthroughAdapter_BuildRequest_UsesInboundContentType(t *testing.T) {
	a := &PassthroughAdapter{}
	in := provider.BuildInput{
		InboundBody:        []byte("--huakai-boundary\r\n"),
		InboundContentType: "multipart/form-data; boundary=huakai-boundary",
		EndpointPath:       "/v1/audio/transcriptions",
		Credential:         provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-x"},
		Account:            provider.AccountInfo{AccountID: 42, Platform: "openai", AccountType: "apikey"},
		UpstreamModelID:    "whisper-1",
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Content-Type"); got != "multipart/form-data; boundary=huakai-boundary" {
		t.Fatalf("Content-Type=%q want inbound multipart boundary", got)
	}
	if got := req.URL.Path; got != "/v1/audio/transcriptions" {
		t.Fatalf("path=%q want /v1/audio/transcriptions", got)
	}
}

func TestPassthroughAdapter_BuildRequest_OrgAndProject(t *testing.T) {
	a := &PassthroughAdapter{}
	in := provider.BuildInput{
		InboundBody: []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "sk-x",
			Extra: map[string]string{
				"org_id":      "org-123",
				"project_id":  "proj-456",
				"openai_beta": "responses=v1",
			},
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("OpenAI-Organization"); got != "org-123" {
		t.Errorf("OpenAI-Organization=%q", got)
	}
	if got := req.Header.Get("OpenAI-Project"); got != "proj-456" {
		t.Errorf("OpenAI-Project=%q", got)
	}
	if got := req.Header.Get("OpenAI-Beta"); got != "responses=v1" {
		t.Errorf("OpenAI-Beta=%q", got)
	}
}

func TestPassthroughAdapter_BuildRequest_CustomEndpoint(t *testing.T) {
	a := &PassthroughAdapter{Endpoint: "https://my-proxy.example.com/v1/chat/completions"}
	in := provider.BuildInput{
		InboundBody: []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "sk-x",
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.String() != "https://my-proxy.example.com/v1/chat/completions" {
		t.Errorf("URL=%q", req.URL.String())
	}
}

func TestPassthroughAdapter_BuildRequest_UpstreamPassthrough(t *testing.T) {
	a := &PassthroughAdapter{}
	in := provider.BuildInput{
		InboundBody: []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeUpstreamPassthrough,
			Value: "Bearer custom-prefix-token",
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer custom-prefix-token" {
		t.Errorf("upstream passthrough 应保留完整 Authorization 值: got=%q", got)
	}
}

func TestAzureApiVersionExtra(t *testing.T) {
	// MUTATION: 忽略 Credential.Extra["azure_api_version"] 时 URL 不带
	// api-version, Azure OpenAI 请求会打到缺版本的 endpoint。
	a := &PassthroughAdapter{}
	in := provider.BuildInput{
		InboundBody: []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeUpstreamPassthrough,
			Value: "Bearer azure-token",
			Extra: map[string]string{
				"base_url":          "https://azure.example/openai/deployments/prod-chat/chat/completions",
				"azure_api_version": "2024-08-01",
			},
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.URL.Query().Get("api-version"); got != "2024-08-01" {
		t.Fatalf("api-version=%q want 2024-08-01; endpoint=%s", got, req.URL.String())
	}
}

func TestPassthroughAdapter_BuildRequest_RejectUnsupportedCredential(t *testing.T) {
	a := &PassthroughAdapter{}
	in := provider.BuildInput{
		InboundBody: []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeOAuthAccessToken,
			Value: "tok-not-supported-here",
		},
	}
	_, err := a.BuildRequest(context.Background(), in)
	if err == nil {
		t.Fatal("OAuth access token 应被 reject")
	}
	if !strings.Contains(err.Error(), "不支持的凭据形态") {
		t.Errorf("error 文案不对: %v", err)
	}
}

func TestPassthroughAdapter_BuildRequest_RejectEmptyValue(t *testing.T) {
	a := &PassthroughAdapter{}
	in := provider.BuildInput{
		InboundBody: []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeAPIKey,
			Value: "",
		},
	}
	_, err := a.BuildRequest(context.Background(), in)
	if err == nil {
		t.Fatal("空 Value 应被 reject")
	}
	if !strings.Contains(err.Error(), "凭据 Value 为空") {
		t.Errorf("error 文案不对: %v", err)
	}
}

func TestPassthroughAdapter_BuildRequest_RespectsContext(t *testing.T) {
	a := &PassthroughAdapter{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	in := provider.BuildInput{
		InboundBody: []byte(`{}`),
		Credential:  provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-x"},
	}
	req, err := a.BuildRequest(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	// req.Context() 已绑定 cancelled ctx；下游 Do() 时会立即 abort
	if req.Context().Err() == nil {
		t.Errorf("request context 未携带 cancellation 信号")
	}
}
