// UpstreamDispatcher 单元测试：mock AdapterRegistry + mock HTTPDoer。
package gateway

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

// stubAdapter 是测试用的最小 Adapter 实现。
type stubAdapter struct {
	platform     string
	endpoint     string
	buildErr     error
	extraHeaders http.Header
	lastInput    provider.BuildInput
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
	for name, values := range s.extraHeaders {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
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

type dispatcherRoundTripFunc func(*http.Request) (*http.Response, error)

func (f dispatcherRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
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

func TestDispatcher_PassesInboundContentTypeToAdapter(t *testing.T) {
	doer := &stubDoer{respStatus: 200, respBody: "{}"}
	adapter := &stubAdapter{platform: "openai"}
	d := newDispatcherForTest(adapter, doer)

	_, err := d.Dispatch(context.Background(), DispatchInput{
		ProtocolFamily:     "openai_chat",
		EndpointPath:       "/v1/audio/transcriptions",
		UpstreamModelID:    "whisper-1",
		InboundBody:        []byte("--same-boundary\r\n"),
		InboundContentType: "multipart/form-data; boundary=same-boundary",
		Account:            provider.AccountInfo{AccountID: 7, Platform: "openai", AccountType: "apikey"},
		Credential:         provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := adapter.lastInput.InboundContentType; got != "multipart/form-data; boundary=same-boundary" {
		t.Fatalf("adapter InboundContentType=%q want inbound multipart boundary", got)
	}
	if got := adapter.lastInput.EndpointPath; got != "/v1/audio/transcriptions" {
		t.Fatalf("adapter EndpointPath=%q want audio endpoint path", got)
	}
}

func TestDispatcher_StripsHopByHopHeadersBeforeDo(t *testing.T) {
	doer := &stubDoer{respStatus: 200, respBody: "{}"}
	adapter := &stubAdapter{
		platform: "openai",
		extraHeaders: http.Header{
			"Connection":          []string{"upgrade"},
			"Keep-Alive":          []string{"timeout=5"},
			"Proxy-Authenticate":  []string{"Basic"},
			"Proxy-Authorization": []string{"Basic secret"},
			"Te":                  []string{"trailers"},
			"Trailer":             []string{"X-Trailer"},
			"Transfer-Encoding":   []string{"chunked"},
			"Upgrade":             []string{"websocket"},
			"X-Stable":            []string{"keep"},
		},
	}
	d := newDispatcherForTest(adapter, doer)

	_, err := d.Dispatch(context.Background(), DispatchInput{
		ProtocolFamily:  "openai_chat",
		UpstreamModelID: "gpt-4o",
		InboundBody:     []byte("{}"),
		Account:         provider.AccountInfo{AccountID: 7, Platform: "openai", AccountType: "apikey"},
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-x"},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		"Connection",
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Te",
		"Trailer",
		"Transfer-Encoding",
		"Upgrade",
	} {
		if got := doer.got.Header.Get(name); got != "" {
			t.Fatalf("%s reached HTTP Do with value %q", name, got)
		}
	}
	if got := doer.got.Header.Get("X-Stable"); got != "keep" {
		t.Fatalf("X-Stable=%q want keep", got)
	}
	if got := doer.got.Header.Get("Authorization"); got != "Bearer sk-x" {
		t.Fatalf("Authorization=%q want preserved credential header", got)
	}
}

func TestDispatcher_AdapterNotFound(t *testing.T) {
	d := &UpstreamDispatcher{
		Adapters:         &stubRegistry{err: provider.ErrAdapterNotRegistered},
		TransportFactory: transport.NewFactory(),
		HTTPClient:       &stubDoer{},
	}
	_, err := d.Dispatch(context.Background(), DispatchInput{ProtocolFamily: "unknown"})
	if !errors.Is(err, provider.ErrAdapterNotRegistered) {
		t.Errorf("err=%v want wraps provider.ErrAdapterNotRegistered", err)
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

func TestDispatcherNonStreamingResponseHeaderTimeoutClassifiesUpstreamHeaderTimeout(t *testing.T) {
	tf := transport.NewFactory()
	tf.SetStandard(slowHeaderTransport(200*time.Millisecond, `{"ok":true}`))
	d := &UpstreamDispatcher{
		Adapters:         &stubRegistry{adapter: &stubAdapter{platform: "openai", endpoint: "http://upstream.test/v1/test"}},
		TransportFactory: tf,
		Timeouts: TimeoutConfig{
			HeaderToFirstByte: 25 * time.Millisecond,
		},
	}
	started := time.Now()
	_, err := d.Dispatch(context.Background(), DispatchInput{
		ProtocolFamily:       "openai_chat",
		NonStreamingBuffered: true,
		Account:              provider.AccountInfo{AccountID: 7, Platform: "openai", AccountType: "apikey"},
		Credential:           provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-x"},
	})
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("Dispatch succeeded; want response-header timeout")
	}
	decision := ClassifyAttemptDispatchError(err)
	if decision.TransportClass != TransportErrorUpstreamHeaderTimeout {
		t.Fatalf("transport class=%q err=%v want upstream_header_timeout", decision.TransportClass, err)
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("header timeout elapsed=%s, want fail before server writes headers", elapsed)
	}
}

func slowHeaderTransport(delay time.Duration, body string) *http.Transport {
	return &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		go writePipeHTTPResponse(server, delay, body)
		return client, nil
	}}
}

func writePipeHTTPResponse(conn net.Conn, delay time.Duration, body string) {
	defer conn.Close()
	if !drainPipeHTTPRequest(conn) {
		return
	}
	time.Sleep(delay)
	_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: "+
		strconv.Itoa(len(body))+"\r\n\r\n"+body)
}

func drainPipeHTTPRequest(conn net.Conn) bool {
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return false
		}
		if line == "\r\n" {
			go func() { _, _ = io.Copy(io.Discard, reader) }()
			return true
		}
	}
}

func TestDispatcher_RejectsPassthroughEndpointHostnameAliasBeforeDo(t *testing.T) {
	restore := provider.SwapPassthroughEndpointLookupForTesting(func(_ context.Context, network, host string) ([]netip.Addr, error) {
		if network != "ip" {
			t.Fatalf("lookup network=%q, want ip", network)
		}
		if host != "ip6-localhost" {
			t.Fatalf("lookup host=%q, want ip6-localhost", host)
		}
		return []netip.Addr{netip.MustParseAddr("::1")}, nil
	})
	t.Cleanup(restore)

	innerCalled := false
	tf := transport.NewFactory()
	tf.SetStandard(dispatcherRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		innerCalled = true
		if req.Header.Get("Authorization") != "" {
			t.Fatalf("unsafe passthrough request reached transport with Authorization=%q", req.Header.Get("Authorization"))
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
	}))

	d := &UpstreamDispatcher{
		Adapters:         &stubRegistry{adapter: &stubAdapter{platform: "openai", endpoint: "https://ip6-localhost/v1/test"}},
		TransportFactory: tf,
	}
	_, err := d.Dispatch(context.Background(), DispatchInput{
		ProtocolFamily: "openai_chat",
		Account:        provider.AccountInfo{AccountID: 77, Platform: "openai", AccountType: "upstream_static"},
		Credential: provider.Credential{
			Type:  provider.CredentialTypeUpstreamPassthrough,
			Value: "Bearer proxy-secret",
			Extra: map[string]string{"base_url": "https://ip6-localhost/v1"},
		},
	})
	if !errors.Is(err, provider.ErrUnsafePassthroughEndpoint) {
		t.Fatalf("Dispatch error=%v, want ErrUnsafePassthroughEndpoint", err)
	}
	if innerCalled {
		t.Fatal("unsafe passthrough request reached transport")
	}
	if strings.Contains(err.Error(), "ip6-localhost") || strings.Contains(err.Error(), "proxy-secret") {
		t.Fatalf("dispatcher rejection leaked raw hostname or secret: %v", err)
	}
}

func TestDispatcher_RejectsPassthroughDNSRebindAtDial(t *testing.T) {
	lookupCalls := 0
	restore := provider.SwapPassthroughEndpointLookupForTesting(func(_ context.Context, network, host string) ([]netip.Addr, error) {
		if network != "ip" {
			t.Fatalf("lookup network=%q, want ip", network)
		}
		if host != "rebind.example" {
			t.Fatalf("lookup host=%q, want rebind.example", host)
		}
		lookupCalls++
		if lookupCalls == 1 {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	})
	t.Cleanup(restore)

	dialCalled := false
	tf := transport.NewFactory()
	tf.SetStandard(&http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			dialCalled = true
			return nil, errors.New("base dial should not be reached")
		},
	})

	d := &UpstreamDispatcher{
		Adapters:         &stubRegistry{adapter: &stubAdapter{platform: "openai", endpoint: "https://rebind.example/v1/test"}},
		TransportFactory: tf,
	}
	_, err := d.Dispatch(context.Background(), DispatchInput{
		ProtocolFamily: "openai_chat",
		Account:        provider.AccountInfo{AccountID: 78, Platform: "openai", AccountType: "upstream_static"},
		Credential: provider.Credential{
			Type:  provider.CredentialTypeUpstreamPassthrough,
			Value: "Bearer proxy-secret",
			Extra: map[string]string{"base_url": "https://rebind.example/v1"},
		},
	})
	if !errors.Is(err, provider.ErrUnsafePassthroughEndpoint) {
		t.Fatalf("Dispatch error=%v, want ErrUnsafePassthroughEndpoint", err)
	}
	if lookupCalls != 2 {
		t.Fatalf("lookup calls=%d, want preflight + dial-time lookups", lookupCalls)
	}
	if dialCalled {
		t.Fatal("DNS rebind target reached base dial")
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

// recordingProxyResolver 记录 Resolve 调用，便于断言 dispatcher 是否
// 把 accountID 正确透传给 ProxyResolver。
type recordingProxyResolver struct {
	calls    []int64
	proxyURL *url.URL
	err      error
}

func (r *recordingProxyResolver) Resolve(_ context.Context, accountID int64) (*url.URL, error) {
	r.calls = append(r.calls, accountID)
	return r.proxyURL, r.err
}

func TestDispatcher_ApplyProxy_NilResolver(t *testing.T) {
	d := &UpstreamDispatcher{}
	rt := &http.Transport{}
	out, err := d.applyProxy(context.Background(), rt, 7)
	if err != nil {
		t.Fatal(err)
	}
	if out != http.RoundTripper(rt) {
		t.Error("ProxyResolver 未配置时应返回原 rt")
	}
}

func TestDispatcher_ApplyProxy_ZeroAccountID(t *testing.T) {
	res := &recordingProxyResolver{}
	d := &UpstreamDispatcher{ProxyResolver: res}
	rt := &http.Transport{}
	out, err := d.applyProxy(context.Background(), rt, 0)
	if err != nil {
		t.Fatal(err)
	}
	if out != http.RoundTripper(rt) {
		t.Error("accountID==0 应返回原 rt（不查 resolver）")
	}
	if len(res.calls) != 0 {
		t.Errorf("accountID==0 不应触发 Resolve 调用，calls=%v", res.calls)
	}
}

func TestDispatcher_ApplyProxy_NotFoundFallsThrough(t *testing.T) {
	res := &recordingProxyResolver{err: provider.ErrAccountNotFound}
	d := &UpstreamDispatcher{ProxyResolver: res}
	rt := &http.Transport{}
	out, err := d.applyProxy(context.Background(), rt, 42)
	if err != nil {
		t.Fatalf("ErrAccountNotFound 不应作为错误传播: %v", err)
	}
	if out != http.RoundTripper(rt) {
		t.Error("未注册账号应返回原 rt")
	}
	if len(res.calls) != 1 || res.calls[0] != 42 {
		t.Errorf("Resolve 应收到 accountID=42，实际 calls=%v", res.calls)
	}
}

func TestDispatcher_ApplyProxy_DirectConnectExplicit(t *testing.T) {
	// 已注册但 proxyURL=nil → 明确直连，wrap 应返回原 rt
	res := &recordingProxyResolver{proxyURL: nil}
	d := &UpstreamDispatcher{ProxyResolver: res}
	rt := &http.Transport{}
	out, err := d.applyProxy(context.Background(), rt, 5)
	if err != nil {
		t.Fatal(err)
	}
	if out != http.RoundTripper(rt) {
		t.Error("已注册直连账号应返回原 rt（WrapTransportWithProxy nil 短路）")
	}
}

func TestDispatcher_ApplyProxy_WithProxy(t *testing.T) {
	proxyURL, _ := url.Parse("http://proxy.example.com:3128")
	res := &recordingProxyResolver{proxyURL: proxyURL}
	d := &UpstreamDispatcher{ProxyResolver: res}
	rt := &http.Transport{}
	out, err := d.applyProxy(context.Background(), rt, 9)
	if err != nil {
		t.Fatal(err)
	}
	cloned, ok := out.(*http.Transport)
	if !ok {
		t.Fatalf("期望 *http.Transport（Clone），实际 %T", out)
	}
	if cloned == rt {
		t.Error("应是 Clone 出来的新实例")
	}
	if cloned.Proxy == nil {
		t.Error("Proxy func 未设置")
	}
	req, _ := http.NewRequest("GET", "https://example.com", nil)
	got, _ := cloned.Proxy(req)
	if got.String() != proxyURL.String() {
		t.Errorf("Proxy func 返回 %q want %q", got, proxyURL)
	}
}

func TestDispatcher_ApplyProxy_ResolverErrorPropagates(t *testing.T) {
	res := &recordingProxyResolver{err: errors.New("db down")}
	d := &UpstreamDispatcher{ProxyResolver: res}
	rt := &http.Transport{}
	_, err := d.applyProxy(context.Background(), rt, 11)
	if err == nil {
		t.Fatal("非 NotFound 错误应传播")
	}
	if !strings.Contains(err.Error(), "ProxyResolver.Resolve 失败") {
		t.Errorf("err=%v want 含 'ProxyResolver.Resolve 失败'", err)
	}
}

// TestDispatcher_ApplyProxy_MisconfiguredPropagates 安全回归：
// ErrProxyResolverMisconfigured（DI 错误）必须传播，不能 fall-through 为
// 直连——否则所有账号会静默绕过代理，破坏账号级 IP 隔离。
func TestDispatcher_ApplyProxy_MisconfiguredPropagates(t *testing.T) {
	res := &recordingProxyResolver{err: provider.ErrProxyResolverMisconfigured}
	d := &UpstreamDispatcher{ProxyResolver: res}
	rt := &http.Transport{}
	_, err := d.applyProxy(context.Background(), rt, 13)
	if err == nil {
		t.Fatal("ErrProxyResolverMisconfigured 必须传播，绝不能当成直连")
	}
	if !errors.Is(err, provider.ErrProxyResolverMisconfigured) {
		t.Errorf("err=%v want wraps ErrProxyResolverMisconfigured", err)
	}
}

// TestDispatcher_ProxyConsultedInDispatchPath 验证 Dispatch 在
// HTTPClient 为 nil 的生产路径会调用 ProxyResolver.Resolve。
// 用 stubDoer 注入会绕过该路径，因此本测试故意 NOT 注入 HTTPClient，
// 但因没有真实 transport 会真发 HTTP 请求 — 通过 ErrAccountNotFound
// 让 resolver fall-through 后 dispatcher 走原 rt（避免污染网络）。
// 我们只断言 resolver 被调用 + accountID 正确。
func TestDispatcher_ProxyConsultedInDispatchPath(t *testing.T) {
	res := &recordingProxyResolver{err: provider.ErrAccountNotFound}
	adapter := &stubAdapter{platform: "openai", endpoint: "http://127.0.0.1:1/unreachable"}
	d := &UpstreamDispatcher{
		Adapters:         &stubRegistry{adapter: adapter},
		TransportFactory: transport.NewFactory(),
		ProxyResolver:    res,
		// 故意不设 HTTPClient → 走生产路径 → applyProxy 被调用
	}
	_, _ = d.Dispatch(context.Background(), DispatchInput{
		ProtocolFamily:  "openai_chat",
		UpstreamModelID: "gpt-4o",
		InboundBody:     []byte("{}"),
		Account: provider.AccountInfo{
			AccountID: 73, Platform: "openai", AccountType: "apikey",
		},
		Credential: provider.Credential{
			Type: provider.CredentialTypeAPIKey, Value: "sk-x",
		},
	})
	// 断言 resolver 被调用、accountID=73；HTTP Do 失败/成功不重要
	if len(res.calls) != 1 || res.calls[0] != 73 {
		t.Errorf("ProxyResolver 应在生产路径被调用 accountID=73，实际 calls=%v", res.calls)
	}
}

// anthropic 自动 breakpoint 注入测试(cache-p0b)
//
// 这些测试驱动完整的 Dispatch 路径,并断言到达 adapter BuildRequest 的
// 确切字节(stubAdapter.lastInput.InboundBody),即真实的出站 body。
// 它们有区分度:移除
// "客户端已自带 cache_control → 跳过" 这条护栏后,
// TestDispatcher_AnthropicAutoBreakpoints_ClientAlreadyHas 会变红。

func anthropicDispatchInput(body string) DispatchInput {
	return DispatchInput{
		ProtocolFamily:  "anthropic_messages",
		UpstreamModelID: "claude-opus-4-5",
		InboundBody:     []byte(body),
		Account: provider.AccountInfo{
			AccountID: 9, Platform: "anthropic", AccountType: "apikey",
		},
		Credential: provider.Credential{
			Type: provider.CredentialTypeAPIKey, Value: "sk-ant",
		},
	}
}

// (1) opt-in 开 + 客户端未发 cache_control → 出站 body 被注入。
func TestDispatcher_AnthropicAutoBreakpoints_InjectsWhenAbsent(t *testing.T) {
	doer := &stubDoer{respStatus: 200, respBody: "data: ok\n\n"}
	adapter := &stubAdapter{platform: "anthropic"}
	d := newDispatcherForTest(adapter, doer)
	d.AnthropicAutoBreakpoints = true

	body := `{"model":"claude-opus-4-5","system":[{"type":"text","text":"sys"}],` +
		`"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`
	if _, err := d.Dispatch(context.Background(), anthropicDispatchInput(body)); err != nil {
		t.Fatal(err)
	}
	got := string(adapter.lastInput.InboundBody)
	if !strings.Contains(got, "cache_control") {
		t.Fatalf("expected cache_control injected into outbound body, got=%s", got)
	}
	snap, err := InspectCacheControl(adapter.lastInput.InboundBody)
	if err != nil {
		t.Fatalf("inspect injected body: %v", err)
	}
	if snap.Count < 1 {
		t.Fatalf("expected >=1 cache_control breakpoint, got %d", snap.Count)
	}
}

// (2) opt-in 开 + 客户端已带 cache_control → 出站 body 与 inbound body
// 逐字节相同(planner 必须不运行)。
func TestDispatcher_AnthropicAutoBreakpoints_ClientAlreadyHas(t *testing.T) {
	doer := &stubDoer{respStatus: 200, respBody: "data: ok\n\n"}
	adapter := &stubAdapter{platform: "anthropic"}
	d := newDispatcherForTest(adapter, doer)
	d.AnthropicAutoBreakpoints = true

	body := `{"model":"claude-opus-4-5","system":[{"type":"text","text":"sys"}],` +
		`"messages":[{"role":"user","content":[{"type":"text","text":"hi",` +
		`"cache_control":{"type":"ephemeral"}}]}]}`
	if _, err := d.Dispatch(context.Background(), anthropicDispatchInput(body)); err != nil {
		t.Fatal(err)
	}
	if string(adapter.lastInput.InboundBody) != body {
		t.Fatalf("client-supplied cache_control body must pass through unchanged.\n want=%s\n got =%s",
			body, string(adapter.lastInput.InboundBody))
	}
	snap, err := InspectCacheControl(adapter.lastInput.InboundBody)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if snap.Count != 1 {
		t.Fatalf("expected exactly the client's 1 cache_control (no extras), got %d", snap.Count)
	}
}

// (3) opt-in 关 → planner 从不运行,即便没有客户端 cache_control,
// body 也原样穿过。
func TestDispatcher_AnthropicAutoBreakpoints_DisabledNeverInjects(t *testing.T) {
	doer := &stubDoer{respStatus: 200, respBody: "data: ok\n\n"}
	adapter := &stubAdapter{platform: "anthropic"}
	d := newDispatcherForTest(adapter, doer)
	// AnthropicAutoBreakpoints 保持零值(false)。

	body := `{"model":"claude-opus-4-5","system":[{"type":"text","text":"sys"}],` +
		`"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`
	if _, err := d.Dispatch(context.Background(), anthropicDispatchInput(body)); err != nil {
		t.Fatal(err)
	}
	if string(adapter.lastInput.InboundBody) != body {
		t.Fatalf("opt-in disabled: body must be unchanged.\n want=%s\n got =%s",
			body, string(adapter.lastInput.InboundBody))
	}
}

// (附加) opt-in 开但非 anthropic 族 → 永不注入。
func TestDispatcher_AnthropicAutoBreakpoints_OnlyAnthropicFamily(t *testing.T) {
	doer := &stubDoer{respStatus: 200, respBody: "data: ok\n\n"}
	adapter := &stubAdapter{platform: "openai"}
	d := newDispatcherForTest(adapter, doer)
	d.AnthropicAutoBreakpoints = true

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	in := anthropicDispatchInput(body)
	in.ProtocolFamily = "openai_chat"
	if _, err := d.Dispatch(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if string(adapter.lastInput.InboundBody) != body {
		t.Fatalf("non-anthropic family must be untouched, got=%s", string(adapter.lastInput.InboundBody))
	}
}

// 变异:Dispatch 丢掉 InboundBetaTokens→BuildInput 映射 → 红——
// 客户端 anthropic-beta 透传链(DM-03)在 raw dispatch 层断裂。
func TestDispatcher_PassesInboundBetaTokensToAdapter(t *testing.T) {
	doer := &stubDoer{respStatus: 200, respBody: "{}"}
	adapter := &stubAdapter{platform: "anthropic"}
	d := newDispatcherForTest(adapter, doer)

	_, err := d.Dispatch(context.Background(), DispatchInput{
		ProtocolFamily:    "anthropic_messages",
		UpstreamModelID:   "claude-3-5-sonnet-20241022",
		InboundBody:       []byte(`{}`),
		InboundBetaTokens: []string{"context-management-2025-06-27", "interleaved-thinking-2025-05-14"},
		Account:           provider.AccountInfo{AccountID: 7, Platform: "anthropic", AccountType: "apikey"},
		Credential:        provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := adapter.lastInput.InboundBetaTokens
	if len(got) != 2 || got[0] != "context-management-2025-06-27" || got[1] != "interleaved-thinking-2025-05-14" {
		t.Fatalf("adapter InboundBetaTokens=%v; want 完整透传", got)
	}
}

// TestDispatcher_PassesClientStreamIntentToAdapter 守卫跨协议流式意图穿透:
// DispatchInput.ClientStreamIntent 必须原样到达 provider.BuildInput——gemini-shaped
// 族(gemini_messages/vertex_gemini/gemini_code_assist)的流式端点选择依赖它,
// 断链则 openai/anthropic 客户端的流式请求错选非流 :generateContent。
// 变异:删 Dispatch 里 BuildInput 的 ClientStreamIntent 赋值 → 本测试红。
func TestDispatcher_PassesClientStreamIntentToAdapter(t *testing.T) {
	doer := &stubDoer{respStatus: 200, respBody: "{}"}
	adapter := &stubAdapter{platform: "gemini"}
	d := newDispatcherForTest(adapter, doer)

	_, err := d.Dispatch(context.Background(), DispatchInput{
		ProtocolFamily:     "gemini_messages",
		UpstreamModelID:    "gemini-2.5-pro",
		InboundBody:        []byte(`{"contents":[]}`),
		Account:            provider.AccountInfo{AccountID: 7, Platform: "gemini", AccountType: "apikey"},
		Credential:         provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "AIza-x"},
		ClientStreamIntent: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !adapter.lastInput.ClientStreamIntent {
		t.Fatal("adapter ClientStreamIntent=false want true(意图在 dispatcher 断链)")
	}
}
