// 包 windsurf — WindsurfSessionAdapter 烟雾测试。
package windsurf

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestWindsurfSessionAdapter_Platform(t *testing.T) {
	a := &WindsurfSessionAdapter{}
	if got := a.Platform(); got != "windsurf" {
		t.Errorf("Platform()=%q want windsurf", got)
	}
}

func TestWindsurfSessionAdapter_RejectsAPIKey(t *testing.T) {
	a := &WindsurfSessionAdapter{}
	for _, ct := range a.AcceptableCredentialTypes() {
		if ct == provider.CredentialTypeAPIKey {
			t.Error("AcceptableCredentialTypes 不应包含 apikey")
		}
	}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "windsurf-default",
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-x"},
	})
	if err == nil || !strings.Contains(err.Error(), "apikey") {
		t.Errorf("apikey 凭据应被拒绝，err=%v", err)
	}
}

func TestWindsurfSessionAdapter_RejectsEmptyValue(t *testing.T) {
	a := &WindsurfSessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "windsurf-default",
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: ""},
	})
	if err == nil {
		t.Error("空 Credential.Value 应被拒绝")
	}
}

func TestWindsurfSessionAdapter_RejectsEmptyModelID(t *testing.T) {
	a := &WindsurfSessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "",
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "codeium-token"},
	})
	if err == nil {
		t.Error("空 UpstreamModelID 应被拒绝")
	}
}

func TestWindsurfSessionAdapter_NormalSessionReversal_RequestMatchesCurrentPlaceholderContract(t *testing.T) {
	body := []byte(`{"model":"windsurf-default","messages":[{"role":"user","content":"ping"}],"metadata":{"ide":"windsurf"}}`)
	in := provider.BuildInput{
		UpstreamModelID: "windsurf-default",
		InboundBody:     body,
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "ws-token",
			Extra: map[string]string{
				"codeium_extension_version": "1.8.41",
				"codeium_telemetry_tags":    `{"ide":"windsurf","surface":"chat"}`,
				"user_agent":                "Windsurf/1.0.1 (linux; x64; codeium)",
			},
		},
	}

	defaultReq, err := (&WindsurfSessionAdapter{}).BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := defaultReq.URL.String(); got != defaultWindsurfEndpoint {
		t.Fatalf("默认 endpoint=%q want %q", got, defaultWindsurfEndpoint)
	}

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		assertWindsurfPlaceholderRequest(t, r, body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader([]byte(`{"id":"windsurf-fake","choices":[]}`))),
			Request:    r,
		}, nil
	})}

	a := &WindsurfSessionAdapter{Endpoint: "https://fake.windsurf.local/exa/windsurf_v2/chat/completions"}
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

// TODO(provider-session-response):Windsurf vendor-real endpoint/header/body 明确后,
// 在真实响应处理层补 401 reauth flow 判别测试;不以占位 adapter 冒充覆盖。
// TODO(dispatcher-channel-health):Windsurf 5xx 分类与 DLQ retry 应在 dispatcher/channel-health 层补测。

func assertWindsurfPlaceholderRequest(t *testing.T, r *http.Request, wantBody []byte) {
	t.Helper()

	if r.Method != http.MethodPost {
		t.Errorf("Method=%q want POST", r.Method)
	}
	if got := r.URL.Path; got != "/exa/windsurf_v2/chat/completions" {
		t.Errorf("URL path=%q want /exa/windsurf_v2/chat/completions", got)
	}
	headerWant := map[string]string{
		"Authorization":             "Bearer ws-token",
		"Content-Type":              "application/json",
		"Accept":                    "application/json",
		"User-Agent":                "Windsurf/1.0.1 (linux; x64; codeium)",
		"codeium-extension-version": "1.8.41",
		"X-Codeium-Telemetry-Tags":  `{"ide":"windsurf","surface":"chat"}`,
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
