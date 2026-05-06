// UpstreamDispatcher 单元测试：mock AdapterRegistry + mock HTTPDoer。
package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

// stubAdapter 是测试用的最小 Adapter 实现。
type stubAdapter struct {
	platform   string
	endpoint   string
	buildErr   error
	lastInput  provider.BuildInput
}

func (s *stubAdapter) Platform() string { return s.platform }
func (s *stubAdapter) AcceptableCredentialTypes() []provider.CredentialType {
	return []provider.CredentialType{provider.CredentialTypeAPIKey}
}
func (s *stubAdapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	s.lastInput = in
	if s.buildErr != nil {
		return nil, s.buildErr
	}
	endpoint := s.endpoint
	if endpoint == "" {
		endpoint = "https://example.com/v1/test"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(in.InboundBody)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+in.Credential.Value)
	return req, nil
}

// stubRegistry 按 protocolFamily 返回固定 adapter。
type stubRegistry struct {
	adapter provider.Adapter
	err     error
}

func (s *stubRegistry) For(protocolFamily string) (provider.Adapter, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.adapter, nil
}

// stubDoer 捕获 *http.Request 并返回预制 response。
type stubDoer struct {
	got        *http.Request
	respStatus int
	respBody   string
	respErr    error
}

func (s *stubDoer) Do(req *http.Request) (*http.Response, error) {
	s.got = req
	if s.respErr != nil {
		return nil, s.respErr
	}
	return &http.Response{
		StatusCode: s.respStatus,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(s.respBody)),
	}, nil
}

func newDispatcherForTest(adapter provider.Adapter, doer HTTPDoer) *UpstreamDispatcher {
	return &UpstreamDispatcher{
		Adapters:         &stubRegistry{adapter: adapter},
		TransportFactory: transport.NewFactory(),
		HTTPClient:       doer,
	}
}

func TestDispatcher_HappyPath(t *testing.T) {
	doer := &stubDoer{respStatus: 200, respBody: "data: hello\n\n"}
	adapter := &stubAdapter{platform: "openai"}
	d := newDispatcherForTest(adapter, doer)

	res, err := d.Dispatch(context.Background(), DispatchInput{
		ProtocolFamily:  "openai_chat",
		UpstreamModelID: "gpt-4o",
		InboundBody:     []byte(`{"model":"gpt-4o"}`),
		Account: provider.AccountInfo{
			AccountID: 7, Platform: "openai", AccountType: "apikey",
		},
		Credential: provider.Credential{
			Type: provider.CredentialTypeAPIKey, Value: "sk-x",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Errorf("StatusCode=%d", res.StatusCode)
	}
	if res.Headers.Get("Content-Type") != "text/event-stream" {
		t.Errorf("missing content-type")
	}
	body, _ := io.ReadAll(res.UpstreamReader)
	if !strings.Contains(string(body), "hello") {
		t.Errorf("body=%s", body)
	}
	if err := res.Close(); err != nil {
		t.Errorf("Close err=%v", err)
	}
	// 校验 adapter 收到了正确的 input
	if adapter.lastInput.UpstreamModelID != "gpt-4o" {
		t.Errorf("adapter UpstreamModelID=%q", adapter.lastInput.UpstreamModelID)
	}
	// 校验 doer 收到了正确的 request
	if doer.got.Header.Get("Authorization") != "Bearer sk-x" {
		t.Errorf("Authorization 未透传")
	}
}

func TestDispatcher_AdapterNotFound(t *testing.T) {
	d := &UpstreamDispatcher{
		Adapters:         &stubRegistry{err: ErrAdapterNotFound},
		TransportFactory: transport.NewFactory(),
		HTTPClient:       &stubDoer{},
	}
	_, err := d.Dispatch(context.Background(), DispatchInput{ProtocolFamily: "unknown"})
	if !errors.Is(err, ErrAdapterNotFound) {
		t.Errorf("err=%v want wraps ErrAdapterNotFound", err)
	}
}

func TestDispatcher_BuildRequestError(t *testing.T) {
	adapter := &stubAdapter{platform: "openai", buildErr: errors.New("malformed body")}
	d := newDispatcherForTest(adapter, &stubDoer{})
	_, err := d.Dispatch(context.Background(), DispatchInput{
		ProtocolFamily: "openai_chat",
		Account:        provider.AccountInfo{Platform: "openai"},
	})
	if err == nil || !strings.Contains(err.Error(), "BuildRequest 失败") {
		t.Errorf("err=%v", err)
	}
}

func TestDispatcher_HTTPDoFails(t *testing.T) {
	adapter := &stubAdapter{platform: "openai"}
	doer := &stubDoer{respErr: errors.New("connection refused")}
	d := newDispatcherForTest(adapter, doer)
	_, err := d.Dispatch(context.Background(), DispatchInput{
		ProtocolFamily: "openai_chat",
		Account:        provider.AccountInfo{Platform: "openai"},
		Credential:     provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-x"},
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP Do 失败") {
		t.Errorf("err=%v", err)
	}
}

func TestDispatcher_TransportPolicyReject(t *testing.T) {
	// OpenAI account 请求 mimicry mode 应被 transport policy reject
	adapter := &stubAdapter{platform: "openai"}
	d := newDispatcherForTest(adapter, &stubDoer{})
	_, err := d.Dispatch(context.Background(), DispatchInput{
		ProtocolFamily: "openai_chat",
		Account:        provider.AccountInfo{Platform: "openai"},
		Credential:     provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-x"},
		TransportMode:  transport.TransportModeMimicryClaudeCode,
	})
	if err == nil || !errors.Is(err, transport.ErrModeNotAllowedForProvider) {
		t.Errorf("OpenAI + mimicry_claude_code 应被 reject: err=%v", err)
	}
}

func TestDispatcher_DefaultTransportModeIsStandard(t *testing.T) {
	doer := &stubDoer{respStatus: 200, respBody: ""}
	adapter := &stubAdapter{platform: "openai"}
	d := newDispatcherForTest(adapter, doer)
	// TransportMode 留空，应隐式走 standard
	_, err := d.Dispatch(context.Background(), DispatchInput{
		ProtocolFamily: "openai_chat",
		Account:        provider.AccountInfo{Platform: "openai"},
		Credential:     provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-x"},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDispatcher_NilReceiverGuards(t *testing.T) {
	var d *UpstreamDispatcher
	_, err := d.Dispatch(context.Background(), DispatchInput{})
	if err == nil || !strings.Contains(err.Error(), "nil receiver") {
		t.Errorf("err=%v", err)
	}
}

func TestDispatcher_RequiresAdaptersAndFactory(t *testing.T) {
	d1 := &UpstreamDispatcher{TransportFactory: transport.NewFactory()}
	if _, err := d1.Dispatch(context.Background(), DispatchInput{}); err == nil {
		t.Error("缺 Adapters 应报错")
	}
	d2 := &UpstreamDispatcher{Adapters: &stubRegistry{}}
	if _, err := d2.Dispatch(context.Background(), DispatchInput{}); err == nil {
		t.Error("缺 TransportFactory 应报错")
	}
}
