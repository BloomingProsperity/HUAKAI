package mimicry

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// PROXY-02a:账号级代理必须在 uTLS 握手之下拨号,这样出口 IP 来自代理,
// JA3 仍保持伪装指纹。

// TestHTTPConnectDialer_TunnelsThroughProxy 是判别式测试:启动一个最小 HTTP
// CONNECT 代理桩,断言拨号器经代理向目标发 CONNECT(并携带 Proxy-Authorization)。
// 若绕过代理直连,桩收不到 CONNECT,gotTarget 保持空值并转红。
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

func TestHTTPConnectDialerSetDeadlineFailureClosesConn(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	wrapped := &proxySetDeadlineFailConn{Conn: clientConn}
	oldDial := proxyDialContext
	proxyDialContext = func(context.Context, string, string) (net.Conn, error) {
		return wrapped, nil
	}
	defer func() { proxyDialContext = oldDial }()

	pu, _ := url.Parse("http://proxy.local:8080")
	dial := httpConnectDialer(pu)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	conn, err := dial(ctx, "tcp", "origin.test:443")

	if err == nil {
		conn.Close()
		t.Fatal("设置 proxy deadline 失败时应返回错误")
	}
	if !strings.Contains(err.Error(), "set deadline") {
		t.Fatalf("错误应标明 set deadline, got %v", err)
	}
	if wrapped.setDeadlineCalls != 1 {
		t.Fatalf("setDeadlineCalls=%d,want 1", wrapped.setDeadlineCalls)
	}
	if wrapped.closeCalls != 1 {
		t.Fatalf("closeCalls=%d,want 1", wrapped.closeCalls)
	}
}

func TestHTTPConnectDialerClearDeadlineFailureClosesConn(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	wrapped := &proxyClearDeadlineFailConn{Conn: clientConn}
	oldDial := proxyDialContext
	proxyDialContext = func(context.Context, string, string) (net.Conn, error) {
		return wrapped, nil
	}
	defer func() { proxyDialContext = oldDial }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		br := bufio.NewReader(serverConn)
		if _, err := http.ReadRequest(br); err != nil {
			t.Errorf("read CONNECT: %v", err)
			return
		}
		_, _ = io.WriteString(serverConn, "HTTP/1.1 200 Connection established\r\n\r\n")
	}()

	pu, _ := url.Parse("http://proxy.local:8080")
	dial := httpConnectDialer(pu)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	conn, err := dial(ctx, "tcp", "origin.test:443")

	if err == nil {
		conn.Close()
		t.Fatal("清理 proxy deadline 失败时应返回错误")
	}
	if !strings.Contains(err.Error(), "clear deadline") {
		t.Fatalf("错误应标明 clear deadline, got %v", err)
	}
	if wrapped.setDeadlineCalls < 2 {
		t.Fatalf("未走到清理 deadline 分支,setDeadlineCalls=%d", wrapped.setDeadlineCalls)
	}
	if wrapped.closeCalls != 1 {
		t.Fatalf("closeCalls=%d,want 1", wrapped.closeCalls)
	}
	<-done
}

// TestUtlsDialer_DialRawUsesProxyDialer 守护拨号边界:设置 ProxyDialer 后,
// dialRaw 必须经它出站。变异证伪:若 dialRaw 忽略 ProxyDialer 回退到 NetDialer,
// called 会保持 false 并转红,真实出口 IP 会绕过代理。
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

// PROXY-06:socks5 已在伪装链路上支持。
func TestProxyDialerFromURL_Socks5Supported(t *testing.T) {
	for _, raw := range []string{"socks5://127.0.0.1:1080", "socks5h://u:p@127.0.0.1:1080"} {
		pu, _ := url.Parse(raw)
		if _, err := proxyDialerFromURL(pu); err != nil {
			t.Fatalf("socks5 %q must be supported now: %v", raw, err)
		}
	}
}

// 未知 scheme 必须 fail-loud,绝不能静默直连。
func TestProxyDialerFromURL_RejectsUnknownScheme(t *testing.T) {
	pu, _ := url.Parse("quic://127.0.0.1:1080")
	if _, err := proxyDialerFromURL(pu); err == nil {
		t.Fatal("unknown proxy scheme must fail-loud, not silently leak the egress IP")
	}
}

type proxySetDeadlineFailConn struct {
	net.Conn
	mu               sync.Mutex
	setDeadlineCalls int
	closeCalls       int
}

func (c *proxySetDeadlineFailConn) SetDeadline(time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setDeadlineCalls++
	return errors.New("set deadline failed")
}

func (c *proxySetDeadlineFailConn) Close() error {
	c.mu.Lock()
	c.closeCalls++
	c.mu.Unlock()
	return c.Conn.Close()
}

type proxyClearDeadlineFailConn struct {
	net.Conn
	mu               sync.Mutex
	setDeadlineCalls int
	closeCalls       int
}

func (c *proxyClearDeadlineFailConn) SetDeadline(deadline time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setDeadlineCalls++
	if deadline.IsZero() {
		return errors.New("clear deadline failed")
	}
	return c.Conn.SetDeadline(deadline)
}

func (c *proxyClearDeadlineFailConn) Close() error {
	c.mu.Lock()
	c.closeCalls++
	c.mu.Unlock()
	return c.Conn.Close()
}
