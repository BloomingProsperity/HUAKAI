package provider

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"
)

// PROXY-02b(lite): a transport that manages its own TLS via DialTLSContext (the
// mimicry sidecar) makes net/http IGNORE the Proxy func, so Clone()+Proxy would
// SILENTLY drop the per-account proxy and leak the real egress IP. The wrapper
// must fail loud instead.

// TestWrapTransportWithProxy_CustomTLSTransportFailsLoud is the discriminating
// test. WITH the guard: ErrProxyUnsupportedTransport is returned without dialing.
// WITHOUT the guard (mutation): Clone()+Proxy ignores Proxy and dials via the
// custom DialTLSContext -> the sentinel error surfaces -> assertion red, proving
// the real egress IP would have leaked.
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

// A plain *http.Transport (no custom TLS dial) still gets the proxy injected.
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
