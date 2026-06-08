package provider

import (
	"net/http"
	"net/url"
	"testing"
)

// PROXY-02a: WrapTransportWithProxy must let a proxy-aware RoundTripper (e.g. the
// mimicry uTLS dialer) inject the proxy beneath its handshake, instead of
// rejecting it as an unsupported transport.

type fakeProxyAware struct {
	got       *url.URL
	returnErr error
}

func (f *fakeProxyAware) RoundTrip(*http.Request) (*http.Response, error) { return nil, nil }

func (f *fakeProxyAware) WithProxy(u *url.URL) (http.RoundTripper, error) {
	f.got = u
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	return f, nil
}

// MUTATION: removing the proxyAwareRoundTripper branch in WrapTransportWithProxy
// makes f.got stay nil (the RT is rejected via proxyWrappedRoundTripper) -> red.
func TestWrapTransportWithProxy_ProxyAwareInjects(t *testing.T) {
	f := &fakeProxyAware{}
	u, _ := url.Parse("http://user:pass@proxy.test:8080")
	got := WrapTransportWithProxy(f, u)
	if f.got == nil || f.got.String() != u.String() {
		t.Fatalf("proxy-aware RT was not handed the proxy url: got %v", f.got)
	}
	if got != http.RoundTripper(f) {
		t.Fatalf("expected the proxy-aware RT's WithProxy result to be returned")
	}
}

// When the proxy-aware RT cannot build (e.g. unsupported scheme), the wrapper
// must fail-loud at RoundTrip (never a silent direct connection).
func TestWrapTransportWithProxy_ProxyAwareBuildError(t *testing.T) {
	wantErr := errProxyAwareTestBuild
	f := &fakeProxyAware{returnErr: wantErr}
	u, _ := url.Parse("socks5://proxy.test:1080")
	got := WrapTransportWithProxy(f, u)
	_, err := got.RoundTrip(nil)
	if err != wantErr {
		t.Fatalf("expected fail-loud build error %v, got %v", wantErr, err)
	}
}

type proxyAwareTestErr string

func (e proxyAwareTestErr) Error() string { return string(e) }

const errProxyAwareTestBuild proxyAwareTestErr = "proxy build failed (test)"
