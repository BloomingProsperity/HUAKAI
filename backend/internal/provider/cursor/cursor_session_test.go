// 包 cursor — CursorSessionAdapter 烟雾测试。仅覆盖 adapter 协议契约
// （Platform / AcceptableCredentialTypes / BuildRequest 拒 apikey 与必填校验），
// 真实 endpoint / vendor header 行为待 OCAW 抓包后扩充。
package cursor

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestCursorSessionAdapter_Platform(t *testing.T) {
	a := &CursorSessionAdapter{}
	if got := a.Platform(); got != "cursor" {
		t.Errorf("Platform()=%q want cursor", got)
	}
}

func TestCursorSessionAdapter_RejectsAPIKey(t *testing.T) {
	a := &CursorSessionAdapter{}
	for _, ct := range a.AcceptableCredentialTypes() {
		if ct == provider.CredentialTypeAPIKey {
			t.Error("AcceptableCredentialTypes 不应包含 apikey")
		}
	}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "claude-3.5-sonnet",
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-x"},
	})
	if err == nil || !strings.Contains(err.Error(), "apikey") {
		t.Errorf("apikey 凭据应被拒绝，err=%v", err)
	}
}

func TestCursorSessionAdapter_RejectsEmptyValue(t *testing.T) {
	a := &CursorSessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "claude-3.5-sonnet",
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "   "},
	})
	if err == nil {
		t.Error("空白 Credential.Value 应被拒绝")
	}
}

func TestCursorSessionAdapter_RejectsEmptyModelID(t *testing.T) {
	a := &CursorSessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "",
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "sess-token"},
	})
	if err == nil {
		t.Error("空 UpstreamModelID 应被拒绝")
	}
}

func TestCursorSessionAdapter_NormalSessionReversal_RequestMatchesSpec(t *testing.T) {
	body := []byte{0, 0, 0, 0, 19, 'c', 'u', 'r', 's', 'o', 'r', '-', 'c', 'o', 'n', 'n', 'e', 'c', 't', '-', 'b', 'o', 'd', 'y'}
	in := provider.BuildInput{
		UpstreamModelID: "claude-3.5-sonnet",
		InboundBody:     body,
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "cursor-session-token",
			Extra: map[string]string{
				"cursor_checksum":       "checksum-123",
				"cursor_client_version": "0.43.7",
				"cookie":                "WorkosCursorSessionToken=session-cookie",
				"user_agent":            "cursor-editor/0.43.7 (linux; x64)",
			},
		},
	}

	defaultReq, err := (&CursorSessionAdapter{}).BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := defaultReq.URL.String(); got != defaultCursorEndpoint {
		t.Fatalf("默认 endpoint=%q want %q", got, defaultCursorEndpoint)
	}

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		assertCursorRequestMatchesSpec(t, r, body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/connect+proto"}},
			Body:       io.NopCloser(bytes.NewReader([]byte{0, 0, 0, 0, 2, 'o', 'k'})),
			Request:    r,
		}, nil
	})}

	a := &CursorSessionAdapter{Endpoint: "https://fake.cursor.local/aiserver.v1.AiService/StreamChat"}
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

func TestCursorSessionAdapter_ExpiredSessionTriggersReauthFlow(t *testing.T) {
	t.Skip("provider.Adapter 只构造请求；401 过期 session 的 reauth flow 尚未接入 provider 层")
}

func TestCursorSessionAdapter_Upstream5xxEnqueuesDLQRetry(t *testing.T) {
	t.Skip("provider.Adapter 不处理响应；5xx DLQ retry 与不挂账户语义应由 dispatcher/channel-health 层补测")
}

func assertCursorRequestMatchesSpec(t *testing.T, r *http.Request, wantBody []byte) {
	t.Helper()

	if r.Method != http.MethodPost {
		t.Errorf("Method=%q want POST", r.Method)
	}
	if got := r.URL.Path; got != "/aiserver.v1.AiService/StreamChat" {
		t.Errorf("URL path=%q want /aiserver.v1.AiService/StreamChat", got)
	}
	headerWant := map[string]string{
		"Authorization":           "Bearer cursor-session-token",
		"Content-Type":            "application/connect+proto",
		"Accept":                  "application/connect+proto",
		"User-Agent":              "cursor-editor/0.43.7 (linux; x64)",
		"x-cursor-checksum":       "checksum-123",
		"x-cursor-client-version": "0.43.7",
		"Cookie":                  "WorkosCursorSessionToken=session-cookie",
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
		t.Errorf("body bytes=%v want %v", gotBody, wantBody)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
