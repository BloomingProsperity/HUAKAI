// 包 copilot — CopilotSessionAdapter 烟雾测试。
package copilot

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestCopilotSessionAdapter_Platform(t *testing.T) {
	a := &CopilotSessionAdapter{}
	if got := a.Platform(); got != "copilot" {
		t.Errorf("Platform()=%q want copilot", got)
	}
}

func TestCopilotSessionAdapter_RejectsAPIKey(t *testing.T) {
	a := &CopilotSessionAdapter{}
	for _, ct := range a.AcceptableCredentialTypes() {
		if ct == provider.CredentialTypeAPIKey {
			t.Error("AcceptableCredentialTypes 不应包含 apikey")
		}
	}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "gpt-4o",
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-x"},
	})
	if err == nil || !strings.Contains(err.Error(), "apikey") {
		t.Errorf("apikey 凭据应被拒绝，err=%v", err)
	}
}

func TestCopilotSessionAdapter_RejectsEmptyValue(t *testing.T) {
	a := &CopilotSessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "gpt-4o",
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "   "},
	})
	if err == nil {
		t.Error("空白 Credential.Value 应被拒绝")
	}
}

func TestCopilotSessionAdapter_RejectsEmptyModelID(t *testing.T) {
	a := &CopilotSessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "",
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "ghu_xxx"},
	})
	if err == nil {
		t.Error("空 UpstreamModelID 应被拒绝")
	}
}

func TestCopilotSessionAdapter_NormalSessionReversal_RequestMatchesSpec(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"ping"}],"stream":true}`)
	in := provider.BuildInput{
		UpstreamModelID: "gpt-4o",
		InboundBody:     body,
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "ghu_token",
			Extra: map[string]string{
				"cookie":                "_octo=octo; logged_in=yes",
				"editor_plugin_version": "copilot-chat/0.16.2",
				"editor_version":        "vscode/1.90.0",
				"github_api_version":    "2023-07-07",
				"openai_intent":         "conversation-panel",
				"user_agent":            "GithubCopilot/1.186.0 vscode/1.90.0",
			},
		},
	}

	defaultReq, err := (&CopilotSessionAdapter{}).BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := defaultReq.URL.String(); got != defaultCopilotEndpoint {
		t.Fatalf("默认 endpoint=%q want %q", got, defaultCopilotEndpoint)
	}

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		assertCopilotRequestMatchesSpec(t, r, body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader([]byte(`{"id":"chatcmpl-fake","choices":[]}`))),
			Request:    r,
		}, nil
	})}

	a := &CopilotSessionAdapter{Endpoint: "https://fake.copilot.local/chat/completions"}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fake upstream status=%d want 200", resp.StatusCode)
	}
}

// TODO(provider-session-response):401 过期 session 的 reauth flow 应在真实响应处理层补判别测试。
// 本 adapter 只构造请求,不以 skipped 测试函数冒充覆盖。
// TODO(dispatcher-channel-health):5xx DLQ retry 与不挂账户语义应在 dispatcher/channel-health 层补测。

func assertCopilotRequestMatchesSpec(t *testing.T, r *http.Request, wantBody []byte) {
	t.Helper()

	if r.Method != http.MethodPost {
		t.Errorf("Method=%q want POST", r.Method)
	}
	if got := r.URL.Path; got != "/chat/completions" {
		t.Errorf("URL path=%q want /chat/completions", got)
	}
	headerWant := map[string]string{
		"Authorization":         "Bearer ghu_token",
		"Content-Type":          "application/json",
		"Accept":                "application/json",
		"User-Agent":            "GithubCopilot/1.186.0 vscode/1.90.0",
		"Editor-Version":        "vscode/1.90.0",
		"Editor-Plugin-Version": "copilot-chat/0.16.2",
		"OpenAI-Intent":         "conversation-panel",
		"X-Github-Api-Version":  "2023-07-07",
		"Cookie":                "_octo=octo; logged_in=yes",
	}
	for name, want := range headerWant {
		if got := r.Header.Get(name); got != want {
			t.Errorf("%s=%q want %q", name, got, want)
		}
	}
	gotBody, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBody, wantBody) {
		t.Errorf("body=%s want %s", gotBody, wantBody)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
