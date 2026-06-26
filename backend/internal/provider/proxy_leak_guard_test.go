package provider

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"
)

// PROXY-02b(lite)：通过 DialTLSContext 自管 TLS 的 transport(即
// mimicry sidecar)会让 net/http 【忽略】Proxy func，于是 Clone()+Proxy 会
// 【静默】丢弃账号级代理并泄露真实出口 IP。wrapper 必须改为 fail-loud。

// TestWrapTransportWithProxy_CustomTLSTransportFailsLoud 是判别性测试。
// 【带】guard：返回 ErrProxyUnsupportedTransport 且不发起拨号。
// 【不带】guard(变异):Clone()+Proxy 忽略 Proxy 并经自定义 DialTLSContext
// 拨号 -> sentinel 错误浮现 -> 断言变红，证明真实出口 IP 本会泄露。
func TestWrapTransportWithProxy_CustomTLSTransportFailsLoud(t *testing.T) {
	sentinel := errors.New("dialed via custom DialTLSContext (proxy bypassed = IP LEAK)")
	tr := &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return nil, sentinel
		},
	}
	u, _ := url.Parse("http://proxy.test:8080")
	got := WrapTransportWithProxy(tr, u)
	req, _ := http.NewRequest(http.MethodGet, "https://origin.test", nil)
	_, err := got.RoundTrip(req)
	if !errors.Is(err, ErrProxyUnsupportedTransport) {
		t.Fatalf("sidecar-style (DialTLSContext) transport + proxy must fail loud with ErrProxyUnsupportedTransport, got %v — else the real egress IP leaks", err)
	}
}

// 普通 *http.Transport(无自定义 TLS 拨号)仍会被注入代理。
func TestWrapTransportWithProxy_PlainTransportGetsProxy(t *testing.T) {
	tr := &http.Transport{}
	u, _ := url.Parse("http://proxy.test:8080")
	got, ok := WrapTransportWithProxy(tr, u).(*http.Transport)
	if !ok {
		t.Fatal("plain transport should remain *http.Transport")
	}
	if got.Proxy == nil {
		t.Fatal("plain transport should get Proxy set")
	}
	pu, err := got.Proxy(nil)
	if err != nil || pu == nil || pu.String() != u.String() {
		t.Fatalf("proxy url not set correctly: pu=%v err=%v", pu, err)
	}
}
