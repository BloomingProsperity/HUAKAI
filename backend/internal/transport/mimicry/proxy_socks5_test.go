package mimicry

import (
	"context"
	"io"
	"net"
	"net/url"
	"testing"
	"time"
)

// PROXY-06:SOCKS5 dialer 必须完成一次真实的 SOCKS5 握手(method negotiation +
// user/pass auth + CONNECT)并隧道到目标,使 uTLS 伪装能经住宅 SOCKS5 池出口。
func TestSocks5Dialer_TunnelsThroughProxy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var gotHost, gotUser, gotPass string
	var gotPort int
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		// 握手问候:ver、nmethods、methods...
		g := make([]byte, 2)
		if _, err := io.ReadFull(c, g); err != nil {
			return
		}
		methods := make([]byte, int(g[1]))
		_, _ = io.ReadFull(c, methods)
		// 选择 user/pass auth(0x02)
		_, _ = c.Write([]byte{0x05, 0x02})
		// 认证:ver、ulen、user、plen、pass
		ah := make([]byte, 2)
		_, _ = io.ReadFull(c, ah)
		ub := make([]byte, int(ah[1]))
		_, _ = io.ReadFull(c, ub)
		gotUser = string(ub)
		pl := make([]byte, 1)
		_, _ = io.ReadFull(c, pl)
		pb := make([]byte, int(pl[0]))
		_, _ = io.ReadFull(c, pb)
		gotPass = string(pb)
		_, _ = c.Write([]byte{0x01, 0x00}) // auth 成功
		// 连接:ver、cmd、rsv、atyp(0x03 domain)、len、host、port
		h := make([]byte, 4)
		_, _ = io.ReadFull(c, h)
		if h[3] == 0x03 {
			l := make([]byte, 1)
			_, _ = io.ReadFull(c, l)
			host := make([]byte, int(l[0]))
			_, _ = io.ReadFull(c, host)
			gotHost = string(host)
		}
		p := make([]byte, 2)
		_, _ = io.ReadFull(c, p)
		gotPort = int(p[0])<<8 | int(p[1])
		// 回成功响应:ver、rep、rsv、atyp(ipv4)、0.0.0.0、0
		_, _ = c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		_, _ = io.Copy(io.Discard, c)
	}()

	pu, _ := url.Parse("socks5://alice:secret@" + ln.Addr().String())
	dial, err := proxyDialerFromURL(pu)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dial(ctx, "tcp", "origin.test:443")
	if err != nil {
		t.Fatalf("socks5 dial: %v", err)
	}
	_ = conn.Close()
	<-done

	if gotHost != "origin.test" || gotPort != 443 {
		t.Fatalf("socks5 CONNECT target=%s:%d want origin.test:443 (handshake wrong)", gotHost, gotPort)
	}
	if gotUser != "alice" || gotPass != "secret" {
		t.Fatalf("socks5 auth user/pass=%q/%q want alice/secret", gotUser, gotPass)
	}
}
