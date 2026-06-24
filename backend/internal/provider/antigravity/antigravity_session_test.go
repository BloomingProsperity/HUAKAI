// 包 antigravity — AntigravitySessionAdapter 烟雾测试。
package antigravity

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestAntigravitySessionAdapter_Platform(t *testing.T) {
	a := &AntigravitySessionAdapter{}
	if got := a.Platform(); got != "antigravity" {
		t.Errorf("Platform()=%q want antigravity", got)
	}
}

func TestAntigravitySessionAdapter_RejectsAPIKey(t *testing.T) {
	a := &AntigravitySessionAdapter{}
	for _, ct := range a.AcceptableCredentialTypes() {
		if ct == provider.CredentialTypeAPIKey {
			t.Error("AcceptableCredentialTypes 不应包含 apikey")
		}
	}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "antigravity-default",
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-x"},
	})
	if err == nil || !strings.Contains(err.Error(), "apikey") {
		t.Errorf("apikey 凭据应被拒绝，err=%v", err)
	}
}

func TestAntigravitySessionAdapter_RejectsEmptyValue(t *testing.T) {
	a := &AntigravitySessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "antigravity-default",
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: " "},
	})
	if err == nil {
		t.Error("空白 Credential.Value 应被拒绝")
	}
}

func TestAntigravitySessionAdapter_RejectsEmptyModelID(t *testing.T) {
	a := &AntigravitySessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "",
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "ag-token"},
	})
	if err == nil {
		t.Error("空 UpstreamModelID 应被拒绝")
	}
}

func TestAntigravitySessionAdapter_NormalSessionReversal_RequestMatchesCurrentPlaceholderContract(t *testing.T) {
	body := []byte(`{"model":"antigravity-default","messages":[{"role":"user","content":"ping"}]}`)
	in := provider.BuildInput{
		UpstreamModelID: "antigravity-default",
		InboundBody:     body,
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "ag-session",
			Extra: map[string]string{
				"cookie":     "ag_session_cookie=abc",
				"user_agent": "antigravity-client/1.0.1",
			},
		},
	}

	defaultReq, err := (&AntigravitySessionAdapter{}).BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := defaultReq.URL.String(); got != defaultAntigravityEndpoint {
		t.Fatalf("默认 endpoint=%q want %q", got, defaultAntigravityEndpoint)
	}

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		assertAntigravityPlaceholderRequest(t, r, body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader([]byte(`{"id":"ag-fake","choices":[]}`))),
			Request:    r,
		}, nil
	})}

	a := &AntigravitySessionAdapter{Endpoint: "https://fake.antigravity.local/v1/chat/completions"}
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

// TODO(provider-session-response):Antigravity vendor-real endpoint/header/body 明确后,
// 在真实响应处理层补 401 reauth flow 判别测试;不以占位 adapter 冒充覆盖。
// TODO(dispatcher-channel-health):Antigravity 5xx 分类与 DLQ retry 应在 dispatcher/channel-health 层补测。

func assertAntigravityPlaceholderRequest(t *testing.T, r *http.Request, wantBody []byte) {
	t.Helper()

	if r.Method != http.MethodPost {
		t.Errorf("Method=%q want POST", r.Method)
	}
	if got := r.URL.Path; got != "/v1/chat/completions" {
		t.Errorf("URL path=%q want /v1/chat/completions", got)
	}
	headerWant := map[string]string{
		"Authorization": "Bearer ag-session",
		"Content-Type":  "application/json",
		"Accept":        "application/json",
		"User-Agent":    "antigravity-client/1.0.1",
		"Cookie":        "ag_session_cookie=abc",
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
