// proxy_resolver_test.go — StaticProxyResolver + WrapTransportWithProxy 单元测试。
package provider

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"testing"
)

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("解析 URL %q：%v", raw, err)
	}
	return u
}

func TestStaticProxyResolver_SetAndResolve(t *testing.T) {
	r := NewStaticProxyResolver()
	proxyURL := mustParseURL(t, "http://proxy.example.com:3128")
	if err := r.Set(42, proxyURL); err != nil {
		t.Fatal(err)
	}
	got, err := r.Resolve(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != proxyURL.String() {
		t.Errorf("Resolve=%q want %q", got, proxyURL)
	}
}

func TestStaticProxyResolver_DirectConnectExplicit(t *testing.T) {
	// 已注册但 nil URL = 直连（已知，与"未注册"不同）
	r := NewStaticProxyResolver()
	if err := r.Set(7, nil); err != nil {
		t.Fatal(err)
	}
	got, err := r.Resolve(context.Background(), 7)
	if err != nil {
		t.Errorf("err=%v want nil", err)
	}
	if got != nil {
		t.Errorf("got=%v want nil（直连）", got)
	}
}

func TestStaticProxyResolver_NotFound(t *testing.T) {
	r := NewStaticProxyResolver()
	_, err := r.Resolve(context.Background(), 999)
	if !errors.Is(err, ErrAccountNotFound) {
		t.Errorf("err=%v want ErrAccountNotFound", err)
	}
}

func TestStaticProxyResolver_NilReceiver(t *testing.T) {
	// nil receiver 是 DI 错误，必须 fail-loud 为 ErrProxyResolverMisconfigured
	// （不能返回 ErrAccountNotFound，否则 dispatcher 把它当成直连
	// → fail-open 绕过代理 → 破坏账号级 IP 隔离）
	var r *StaticProxyResolver
	_, err := r.Resolve(context.Background(), 1)
	if !errors.Is(err, ErrProxyResolverMisconfigured) {
		t.Errorf("nil receiver err=%v want ErrProxyResolverMisconfigured", err)
	}
	if errors.Is(err, ErrAccountNotFound) {
		t.Error("nil receiver 不应混淆为 ErrAccountNotFound（fail-open 风险）")
	}
	if r.Size() != 0 {
		t.Errorf("nil receiver Size=%d want 0", r.Size())
	}
}

func TestStaticProxyResolver_SetRejectsZeroID(t *testing.T) {
	r := NewStaticProxyResolver()
	err := r.Set(0, mustParseURL(t, "http://x"))
	if err == nil {
		t.Error("accountID==0 应被 reject")
	}
}

func TestStaticProxyResolver_Size(t *testing.T) {
	r := NewStaticProxyResolver()
	if r.Size() != 0 {
		t.Errorf("空 size=%d", r.Size())
	}
	_ = r.Set(1, nil)
	_ = r.Set(2, mustParseURL(t, "http://x"))
	if r.Size() != 2 {
		t.Errorf("size=%d want 2", r.Size())
	}
}

func TestStaticProxyResolver_Concurrent(t *testing.T) {
	r := NewStaticProxyResolver()
	for i := int64(1); i <= 10; i++ {
		_ = r.Set(i, nil)
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := int64((idx % 10) + 1)
			_, _ = r.Resolve(context.Background(), id)
		}(i)
	}
	wg.Wait()
}

func TestWrapTransportWithProxy_NilProxy(t *testing.T) {
	rt := &http.Transport{}
	got := WrapTransportWithProxy(rt, nil)
	if got != rt {
		t.Errorf("nil proxy 应返回原 rt 不变")
	}
}

func TestWrapTransportWithProxy_StandardTransport(t *testing.T) {
	rt := &http.Transport{}
	proxyURL := mustParseURL(t, "http://proxy.example.com:3128")
	wrapped := WrapTransportWithProxy(rt, proxyURL)

	cloned, ok := wrapped.(*http.Transport)
	if !ok {
		t.Fatalf("wrapped 应是 *http.Transport，实际 %T", wrapped)
	}
	if cloned == rt {
		t.Error("应是 Clone 出来的新实例，不能影响原 rt")
	}
	if cloned.Proxy == nil {
		t.Error("Proxy func 未设置")
	}
	// 验证 Proxy func 真返回了我们设的 URL
	req, _ := http.NewRequest("GET", "https://example.com", nil)
	out, err := cloned.Proxy(req)
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != proxyURL.String() {
		t.Errorf("Proxy func 返回 %q want %q", out, proxyURL)
	}
}

// fakeRoundTripper 是非 *http.Transport 的 RoundTripper 实现，用于验证
// wrap 路径。
type fakeRoundTripper struct{}

func (f *fakeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, errors.New("fake roundtrip")
}

func TestWrapTransportWithProxy_NonStandardTransport(t *testing.T) {
	rt := &fakeRoundTripper{}
	proxyURL := mustParseURL(t, "http://proxy.example.com:3128")
	wrapped := WrapTransportWithProxy(rt, proxyURL)

	pw, ok := wrapped.(*proxyWrappedRoundTripper)
	if !ok {
		t.Fatalf("非 *http.Transport 应被 wrap，实际 %T", wrapped)
	}
	if pw.ProxyURL().String() != proxyURL.String() {
		t.Errorf("ProxyURL=%q want %q", pw.ProxyURL(), proxyURL)
	}
	if pw.inner != rt {
		t.Error("inner 应是原 rt")
	}
}
