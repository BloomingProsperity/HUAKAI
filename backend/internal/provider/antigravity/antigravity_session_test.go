package antigravity

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func antigravityInput(body []byte, extra map[string]string) provider.BuildInput {
	return provider.BuildInput{
		UpstreamModelID: "gemini-3-flash",
		InboundBody:     body,
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "ag-access-token",
			Extra: extra,
		},
	}
}

func TestAntigravitySessionAdapterPlatformAndCredentialTypes(t *testing.T) {
	a := &AntigravitySessionAdapter{}
	if got := a.Platform(); got != "antigravity" {
		t.Fatalf("Platform()=%q，期望 antigravity", got)
	}
	want := map[provider.CredentialType]bool{
		provider.CredentialTypeSessionToken:        true,
		provider.CredentialTypeOAuthAccessToken:    true,
		provider.CredentialTypeUpstreamPassthrough: true,
	}
	for _, got := range a.AcceptableCredentialTypes() {
		delete(want, got)
	}
	if len(want) != 0 {
		t.Fatalf("缺少 Cloud Code OAuth 凭据类型：%v", want)
	}
}

// TestAntigravitySessionRequestUsesCloudCodeProfile 同时守住正式端点、静态 UA、
// X-Goog-Api-Client、Cloud Code envelope 与 GOOGLE_ONE_AI 注入。
// 退回 api.antigravity.ai、删除 credits 或绕过共享 adapter 均会变红。
func TestAntigravitySessionRequestUsesCloudCodeProfile(t *testing.T) {
	inner := []byte(`{"contents":[{"role":"user","parts":[{"text":"ping"}]}]}`)
	req, err := (&AntigravitySessionAdapter{}).BuildRequest(context.Background(), antigravityInput(inner, map[string]string{
		"project_id": "ag-project",
	}))
	if err != nil {
		t.Fatalf("BuildRequest 失败：%v", err)
	}
	if got := req.URL.String(); got != "https://cloudcode-pa.googleapis.com/v1internal:generateContent" {
		t.Fatalf("URL=%q，期望正式 Cloud Code v1internal generateContent", got)
	}
	wantHeaders := map[string]string{
		"Authorization":     "Bearer ag-access-token",
		"Content-Type":      "application/json",
		"Accept":            "application/json",
		"User-Agent":        defaultAntigravityUserAgent,
		"X-Goog-Api-Client": defaultAntigravityAPIClient,
	}
	for name, want := range wantHeaders {
		if got := req.Header.Get(name); got != want {
			t.Errorf("%s=%q，期望 %q", name, got, want)
		}
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("读取请求体失败：%v", err)
	}
	var envelope struct {
		Model              string          `json:"model"`
		Project            string          `json:"project"`
		Request            json.RawMessage `json:"request"`
		EnabledCreditTypes []string        `json:"enabledCreditTypes"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("请求体不是合法 Cloud Code envelope：%v；body=%s", err, body)
	}
	if envelope.Model != "gemini-3-flash" || envelope.Project != "ag-project" {
		t.Fatalf("envelope model/project=(%q,%q)，期望 (gemini-3-flash,ag-project)", envelope.Model, envelope.Project)
	}
	if !jsonBytesEqual(envelope.Request, inner) {
		t.Fatalf("envelope.request=%s，期望 %s", envelope.Request, inner)
	}
	if len(envelope.EnabledCreditTypes) != 1 || envelope.EnabledCreditTypes[0] != antigravityGoogleOneCreditType {
		t.Fatalf("enabledCreditTypes=%v，期望 [GOOGLE_ONE_AI]", envelope.EnabledCreditTypes)
	}
}

// TestAntigravitySessionStreamUsesCloudCodeSSE 守住流式动作与 alt=sse。
func TestAntigravitySessionStreamUsesCloudCodeSSE(t *testing.T) {
	req, err := (&AntigravitySessionAdapter{}).BuildRequest(context.Background(), antigravityInput([]byte(`{"contents":[]}`), map[string]string{
		"project_id": "ag-project",
		"stream":     "true",
	}))
	if err != nil {
		t.Fatalf("BuildRequest 失败：%v", err)
	}
	if got := req.URL.String(); got != "https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse" {
		t.Fatalf("流式 URL=%q，期望 Cloud Code streamGenerateContent?alt=sse", got)
	}
	if got := req.Header.Get("Accept"); got != "text/event-stream" {
		t.Fatalf("Accept=%q，期望 text/event-stream", got)
	}
}

func TestAntigravitySessionEndpointOverrideUsesBaseSemantics(t *testing.T) {
	a := &AntigravitySessionAdapter{Endpoint: "https://cloudcode.test"}
	req, err := a.BuildRequest(context.Background(), antigravityInput([]byte(`{}`), map[string]string{"project_id": "p"}))
	if err != nil {
		t.Fatalf("BuildRequest 失败：%v", err)
	}
	if got := req.URL.String(); got != "https://cloudcode.test/v1internal:generateContent" {
		t.Fatalf("覆盖 URL=%q", got)
	}
}

func TestAntigravitySessionRejectsAPIKeyAndMissingProject(t *testing.T) {
	a := &AntigravitySessionAdapter{}
	in := antigravityInput([]byte(`{}`), map[string]string{"project_id": "p"})
	in.Credential.Type = provider.CredentialTypeAPIKey
	if _, err := a.BuildRequest(context.Background(), in); err == nil || !strings.Contains(err.Error(), "不支持的凭据形态") {
		t.Fatalf("API key 应被拒绝，err=%v", err)
	}
	in = antigravityInput([]byte(`{}`), nil)
	if _, err := a.BuildRequest(context.Background(), in); err == nil || !strings.Contains(err.Error(), "project_id") {
		t.Fatalf("缺少 project_id 应 fail loud，err=%v", err)
	}
}

func jsonBytesEqual(a, b []byte) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
