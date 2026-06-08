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

// PROXY-02a: per-account proxy must be dialed BENEATH the uTLS handshake so the
// egress IP is the proxy's while the JA3 stays the mimicry fingerprint.

// TestHTTPConnectDialer_TunnelsThroughProxy is the discriminating test: it spins
// a minimal HTTP CONNECT proxy stub and asserts the dialer issues a CONNECT to
// the intended target THROUGH the proxy (with Proxy-Authorization). If the dial
// bypassed the proxy, the stub would never see the CONNECT and gotTarget stays
// empty -> red.
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

// TestUtlsDialer_DialRawUsesProxyDialer guards the seam: with a ProxyDialer set,
// dialRaw must route through it. MUTATION: making dialRaw ignore ProxyDialer
// (revert to NetDialer) -> called stays false -> red, i.e. the real egress IP
// would leak past the proxy.
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

// TestProxyDialerFromURL_RejectsSocks5 keeps socks5 fail-loud on the mimicry
// path (we must never silently fall back to a direct connection that leaks IP).
func TestProxyDialerFromURL_RejectsSocks5(t *testing.T) {
	pu, _ := url.Parse("socks5://127.0.0.1:1080")
	if _, err := proxyDialerFromURL(pu); err == nil {
		t.Fatal("socks5 must fail-loud on the mimicry path, not silently leak the egress IP")
	}
}
