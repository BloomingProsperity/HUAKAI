// 包 kiro — KiroSessionAdapter 烟雾测试。
package kiro

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestKiroSessionAdapter_Platform(t *testing.T) {
	a := &KiroSessionAdapter{}
	if got := a.Platform(); got != "kiro" {
		t.Errorf("Platform()=%q want kiro", got)
	}
}

func TestKiroSessionAdapter_RejectsAPIKey(t *testing.T) {
	a := &KiroSessionAdapter{}
	for _, ct := range a.AcceptableCredentialTypes() {
		if ct == provider.CredentialTypeAPIKey {
			t.Error("AcceptableCredentialTypes 不应包含 apikey")
		}
	}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "kiro-default",
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-x"},
	})
	if err == nil || !strings.Contains(err.Error(), "apikey") {
		t.Errorf("apikey 凭据应被拒绝，err=%v", err)
	}
}

func TestKiroSessionAdapter_RejectsEmptyValue(t *testing.T) {
	a := &KiroSessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "kiro-default",
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: ""},
	})
	if err == nil {
		t.Error("空 Credential.Value 应被拒绝")
	}
}

func TestKiroSessionAdapter_RejectsEmptyModelID(t *testing.T) {
	a := &KiroSessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		UpstreamModelID: "",
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "cognito-token"},
	})
	if err == nil {
		t.Error("空 UpstreamModelID 应被拒绝")
	}
}

func TestKiroSessionAdapter_NormalSessionReversal_RequestMatchesCurrentPlaceholderContract(t *testing.T) {
	body := []byte(`{"model":"kiro-default","messages":[{"role":"user","content":"ping"}]}`)
	in := provider.BuildInput{
		UpstreamModelID: "kiro-default",
		InboundBody:     body,
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "cognito-id-token",
			Extra: map[string]string{
				"amzn_request_id":  "00000000-0000-4000-8000-000000000001",
				"cognito_id_token": "cognito-id-token",
				"user_agent":       "Kiro/1.0.1 (linux; x64; aws)",
			},
		},
	}

	defaultReq, err := (&KiroSessionAdapter{}).BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got := defaultReq.URL.String(); got != defaultKiroEndpoint {
		t.Fatalf("默认 endpoint=%q want %q", got, defaultKiroEndpoint)
	}

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		assertKiroPlaceholderRequest(t, r, body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader([]byte(`{"id":"kiro-fake","choices":[]}`))),
			Request:    r,
		}, nil
	})}

	a := &KiroSessionAdapter{Endpoint: "https://fake.kiro.local/v1/chat/completions"}
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

func TestKiroSessionAdapter_ExpiredSessionTriggersReauthFlow(t *testing.T) {
	t.Skip("Kiro vendor-real endpoint/header/body 仍待 OCAW；401 reauth flow 尚未实现，不能用占位 adapter 冒充覆盖")
}

func TestKiroSessionAdapter_Upstream5xxEnqueuesDLQRetry(t *testing.T) {
	t.Skip("Kiro vendor-real 5xx 分类与 DLQ retry 仍待 dispatcher/channel-health 接入后补测")
}

func assertKiroPlaceholderRequest(t *testing.T, r *http.Request, wantBody []byte) {
	t.Helper()

	if r.Method != http.MethodPost {
		t.Errorf("Method=%q want POST", r.Method)
	}
	if got := r.URL.Path; got != "/v1/chat/completions" {
		t.Errorf("URL path=%q want /v1/chat/completions", got)
	}
	headerWant := map[string]string{
		"Authorization":          "Bearer cognito-id-token",
		"Content-Type":           "application/json",
		"Accept":                 "application/json",
		"User-Agent":             "Kiro/1.0.1 (linux; x64; aws)",
		"x-aws-cognito-id-token": "cognito-id-token",
		"x-amzn-requestid":       "00000000-0000-4000-8000-000000000001",
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
