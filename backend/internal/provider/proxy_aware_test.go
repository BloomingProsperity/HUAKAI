package provider

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// PROXY-02a：WrapTransportWithProxy 必须允许一个 proxy-aware 的 RoundTripper
// （例如伪装用的 uTLS dialer）在其握手之下注入代理，而不是把它当作不支持的
// transport 直接拒绝。

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

// 变异：若移除 WrapTransportWithProxy 中的 proxyAwareRoundTripper 分支，会让
// f.got 保持 nil（该 RT 被走 proxyWrappedRoundTripper 拒绝）-> 测试变红。
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

// 当 proxy-aware 的 RT 无法构建时（例如不支持的 scheme），该 wrapper 必须在
// RoundTrip 处 fail-loud（明确报错），绝不能悄悄退化成直连。
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

func TestWrapPassthroughEndpointTransportProxyConflictHasErrorCode(t *testing.T) {
	// ME-3:自定义上游 endpoint 与账号出口代理组合仍必须 fail-closed,但错误要可区分,
	// 使运维能直接定位配置不兼容。变异证伪:把 WrapPassthroughEndpointTransport
	// 改回 passthroughEndpointBlocked("proxy transport is not allowed"),此处 sentinel
	// 与错误码字符串断言都会变红。
	proxyURL, _ := url.Parse("http://proxy.test:8080")
	base := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	_, err := WrapPassthroughEndpointTransport(base)
	if !errors.Is(err, ErrUnsafePassthroughEndpoint) {
		t.Fatalf("err=%v want ErrUnsafePassthroughEndpoint", err)
	}
	if !errors.Is(err, ErrPassthroughProxyCustomEndpointIncompatible) {
		t.Fatalf("err=%v want ErrPassthroughProxyCustomEndpointIncompatible", err)
	}
	if !strings.Contains(err.Error(), "config_incompatible_proxy_custom_endpoint") {
		t.Fatalf("err=%v missing stable diagnostic code", err)
	}
}

type proxyAwareTestErr string

func (e proxyAwareTestErr) Error() string { return string(e) }

const errProxyAwareTestBuild proxyAwareTestErr = "proxy build failed (test)"
