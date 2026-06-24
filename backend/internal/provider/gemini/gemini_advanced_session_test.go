// 包 gemini — GeminiAdvancedSessionAdapter 烟雾测试。
// 测试函数前缀 GeminiAdvancedSession 区别于既有 PassthroughAdapter 测试。
package gemini

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestGeminiAdvancedSessionAdapter_Platform(t *testing.T) {
	a := &GeminiAdvancedSessionAdapter{}
	if got := a.Platform(); got != "gemini_advanced" {
		t.Errorf("Platform()=%q want gemini_advanced", got)
	}
}

func TestGeminiAdvancedSessionAdapter_RejectsAPIKey(t *testing.T) {
	a := &GeminiAdvancedSessionAdapter{}
	for _, ct := range a.AcceptableCredentialTypes() {
		if ct == provider.CredentialTypeAPIKey {
			t.Error("AcceptableCredentialTypes 不应包含 apikey（apikey 走 PassthroughAdapter）")
		}
	}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "gemini-1.5-pro-latest",
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "AIza-x"},
	})
	if err == nil || !strings.Contains(err.Error(), "apikey") {
		t.Errorf("apikey 凭据应被拒绝，err=%v", err)
	}
}

func TestGeminiAdvancedSessionAdapter_RejectsEmptyValue(t *testing.T) {
	a := &GeminiAdvancedSessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "gemini-1.5-pro-latest",
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: ""},
	})
	if err == nil {
		t.Error("空 Credential.Value 应被拒绝")
	}
}

func TestGeminiAdvancedSessionAdapter_RejectsEmptyModelID(t *testing.T) {
	a := &GeminiAdvancedSessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "",
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "google-cookie"},
	})
	if err == nil {
		t.Error("空 UpstreamModelID 应被拒绝")
	}
}

func TestGeminiAdvancedSessionAdapter_HappyPath_InjectsCookie(t *testing.T) {
	// Gemini Advanced 网页反转用 cookie 鉴权（不是 Bearer Authorization）；
	// SessionToken 模式下 Value 整串写入 Cookie header。
	a := &GeminiAdvancedSessionAdapter{}
	req, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "gemini-1.5-pro-latest",
		InboundBody:     []byte(`{}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "__Secure-1PSID=xxx; __Secure-3PSID=yyy",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Cookie"); got != "__Secure-1PSID=xxx; __Secure-3PSID=yyy" {
		t.Errorf("Cookie=%q want Google session cookie 串", got)
	}
	// 网页反转路径不使用 Bearer
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization=%q want 空（cookie 鉴权路径）", got)
	}
	// X-Origin 必填
	if got := req.Header.Get("X-Origin"); got != "https://gemini.google.com" {
		t.Errorf("X-Origin=%q want gemini.google.com", got)
	}
}

func TestGeminiAdvancedSessionAdapter_NormalSessionReversal_RequestMatchesSpec(t *testing.T) {
	body := []byte(`f.req=%5B%5B%22prompt%22%5D%5D&at=csrf-token`)
	in := provider.BuildInput{
		UpstreamModelID: "gemini-1.5-pro-latest",
		InboundBody:     body,
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "__Secure-1PSID=psid; __Secure-3PSID=3psid",
			Extra: map[string]string{
				"goog_authuser": "1",
				"sapisid_hash":  "1700000000_deadbeef",
				"user_agent":    "Mozilla/5.0 Chrome/124.0.0.0 Safari/537.36",
			},
		},
	}

	defaultReq, err := (&GeminiAdvancedSessionAdapter{}).BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := defaultReq.URL.String(); got != defaultGeminiAdvancedEndpoint {
		t.Fatalf("默认 endpoint=%q want %q", got, defaultGeminiAdvancedEndpoint)
	}

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		assertGeminiAdvancedRequestMatchesSpec(t, r, body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader([]byte(`)]}'` + "\n" + `[[["fake-response"]]]`))),
			Request:    r,
		}, nil
	})}

	a := &GeminiAdvancedSessionAdapter{
		Endpoint: "https://fake.gemini.local/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?bl=boq_assistant-bard-web-server_20260519.00_p0&_reqid=123456&rt=c",
	}
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

func assertGeminiAdvancedRequestMatchesSpec(t *testing.T, r *http.Request, wantBody []byte) {
	t.Helper()

	if r.Method != http.MethodPost {
		t.Errorf("Method=%q want POST", r.Method)
	}
	if got := r.URL.Path; got != "/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate" {
		t.Errorf("URL path=%q want Gemini StreamGenerate path", got)
	}
	if got := r.URL.RawQuery; got != "bl=boq_assistant-bard-web-server_20260519.00_p0&_reqid=123456&rt=c" {
		t.Errorf("URL query=%q want Gemini dynamic query", got)
	}
	headerWant := map[string]string{
		"Authorization":   "SAPISIDHASH 1700000000_deadbeef",
		"Cookie":          "__Secure-1PSID=psid; __Secure-3PSID=3psid",
		"Content-Type":    "application/x-www-form-urlencoded;charset=UTF-8",
		"Accept":          "*/*",
		"User-Agent":      "Mozilla/5.0 Chrome/124.0.0.0 Safari/537.36",
		"X-Goog-Authuser": "1",
		"X-Origin":        "https://gemini.google.com",
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
