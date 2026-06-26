package mimicry

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// PROXY-02a:每账号代理必须在 uTLS 握手【之下】拨号,从而出口 IP 是代理的,
// 而 JA3 仍是伪装指纹。

// TestHTTPConnectDialer_TunnelsThroughProxy 是有区分力的测试:它起一个最小的
// HTTP CONNECT 代理桩,断言 dialer 经代理向目标发出 CONNECT(带
// Proxy-Authorization)。若拨号绕过了代理,桩永远收不到 CONNECT,gotTarget 保持
// 为空 -> 转红。
func TestHTTPConnectDialer_TunnelsThroughProxy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var gotTarget, gotAuth string
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		gotTarget = req.Host
		gotAuth = req.Header.Get("Proxy-Authorization")
		_, _ = io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n")
		_, _ = io.Copy(io.Discard, br)
	}()

	pu, _ := url.Parse("http://user:pass@" + ln.Addr().String())
	dial, err := proxyDialerFromURL(pu)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dial(ctx, "tcp", "origin.test:443")
	if err != nil {
		t.Fatalf("dial through proxy: %v", err)
	}
	_ = conn.Close()
	<-done

	if gotTarget != "origin.test:443" {
		t.Fatalf("proxy CONNECT target=%q want origin.test:443 (dial did NOT go through the proxy)", gotTarget)
	}
	if gotAuth == "" {
		t.Fatalf("Proxy-Authorization missing (proxy creds not forwarded)")
	}
}

// TestUtlsDialer_DialRawUsesProxyDialer 守护这道接缝:设置了 ProxyDialer 时,
// dialRaw 必须经它路由。变异:让 dialRaw 忽略 ProxyDialer(回退到 NetDialer)->
// called 保持 false -> 转红,即真实出口 IP 会越过代理泄露。
func TestUtlsDialer_DialRawUsesProxyDialer(t *testing.T) {
	srv, cli := net.Pipe()
	defer srv.Close()
	called := false
	d := &UtlsDialer{
		ProxyDialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
			called = true
			return cli, nil
		},
	}
	conn, err := d.dialRaw(context.Background(), "tcp", "origin.test:443")
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if !called {
		t.Fatal("dialRaw bypassed ProxyDialer -> real egress IP would leak past the proxy")
	}
}

// PROXY-06:伪装路现已支持 socks5。
func TestProxyDialerFromURL_Socks5Supported(t *testing.T) {
	for _, raw := range []string{"socks5://127.0.0.1:1080", "socks5h://u:p@127.0.0.1:1080"} {
		pu, _ := url.Parse(raw)
		if _, err := proxyDialerFromURL(pu); err != nil {
			t.Fatalf("socks5 %q must be supported now: %v", raw, err)
		}
	}
}

// 未知 scheme 仍然 fail-loud(绝不静默直连)。
func TestProxyDialerFromURL_RejectsUnknownScheme(t *testing.T) {
	pu, _ := url.Parse("quic://127.0.0.1:1080")
	if _, err := proxyDialerFromURL(pu); err == nil {
		t.Fatal("unknown proxy scheme must fail-loud, not silently leak the egress IP")
	}
}
