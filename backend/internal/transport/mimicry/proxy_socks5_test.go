package mimicry

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"testing"
	"time"
)

// PROXY-06:SOCKS5 拨号器必须完成真实 SOCKS5 握手(方法协商 +
// user/pass 认证 + CONNECT),并隧道到目标地址,让 uTLS 伪装链路能经住宅
// SOCKS5 池出站。
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
		// 问候帧:ver,nmethods,methods...
		g := make([]byte, 2)
		if _, err := io.ReadFull(c, g); err != nil {
			return
		}
		methods := make([]byte, int(g[1]))
		_, _ = io.ReadFull(c, methods)
		// 选择 user/pass 认证(0x02)。
		_, _ = c.Write([]byte{0x05, 0x02})
		// 认证帧:ver,ulen,user,plen,pass。
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
		_, _ = c.Write([]byte{0x01, 0x00}) // 认证成功
		// CONNECT 帧:ver,cmd,rsv,atyp(0x03 domain),len,host,port。
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
		// 成功响应:ver,rep,rsv,atyp(ipv4),0.0.0.0,0。
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

func TestSocks5HandshakeCompletesShortWrites(t *testing.T) {
	readScript := []byte{
		0x05, 0x00,
		0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0,
	}
	conn := &scriptedShortWriteConn{
		readBuf:  bytes.NewReader(readScript),
		maxChunk: 1,
	}

	if err := socks5Handshake(conn, nil, "origin.test", 443); err != nil {
		t.Fatalf("socks5Handshake 短写入应被完整补写: %v", err)
	}

	want := []byte{0x05, 0x01, 0x00}
	want = append(want, 0x05, 0x01, 0x00, 0x03, byte(len("origin.test")))
	want = append(want, []byte("origin.test")...)
	want = append(want, 0x01, 0xbb)
	if !bytes.Equal(conn.writes, want) {
		t.Fatalf("SOCKS5 写入字节=%v,want %v", conn.writes, want)
	}
	if conn.writeCalls <= 2 {
		t.Fatalf("短写入路径未被触发,writeCalls=%d", conn.writeCalls)
	}
}

func TestSocks5HandshakeRejectsNoProgressWrite(t *testing.T) {
	err := socks5Handshake(socks5NoProgressConn{}, nil, "origin.test", 443)
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("零进展写入错误=%v,want io.ErrShortWrite", err)
	}
}

func TestSocks5HandshakeRejectsNoProgressRead(t *testing.T) {
	err := socks5Handshake(socks5NoProgressReadConn{}, nil, "origin.test", 443)
	if !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("零进展读取错误=%v,want io.ErrNoProgress", err)
	}
}

type scriptedShortWriteConn struct {
	readBuf    *bytes.Reader
	writes     []byte
	maxChunk   int
	writeCalls int
}

func (c *scriptedShortWriteConn) Read(p []byte) (int, error) {
	return c.readBuf.Read(p)
}

func (c *scriptedShortWriteConn) Write(p []byte) (int, error) {
	n := c.maxChunk
	if n <= 0 || n > len(p) {
		n = len(p)
	}
	c.writes = append(c.writes, p[:n]...)
	c.writeCalls++
	return n, nil
}

func (c *scriptedShortWriteConn) Close() error {
	return nil
}

func (c *scriptedShortWriteConn) LocalAddr() net.Addr {
	return socks5TestAddr("local")
}

func (c *scriptedShortWriteConn) RemoteAddr() net.Addr {
	return socks5TestAddr("remote")
}

func (c *scriptedShortWriteConn) SetDeadline(time.Time) error {
	return nil
}

func (c *scriptedShortWriteConn) SetReadDeadline(time.Time) error {
	return nil
}

func (c *scriptedShortWriteConn) SetWriteDeadline(time.Time) error {
	return nil
}

type socks5NoProgressConn struct{}

func (socks5NoProgressConn) Read([]byte) (int, error) {
	return 0, nil
}

func (socks5NoProgressConn) Write([]byte) (int, error) {
	return 0, nil
}

func (socks5NoProgressConn) Close() error {
	return nil
}

func (socks5NoProgressConn) LocalAddr() net.Addr {
	return socks5TestAddr("local")
}

func (socks5NoProgressConn) RemoteAddr() net.Addr {
	return socks5TestAddr("remote")
}

func (socks5NoProgressConn) SetDeadline(time.Time) error {
	return nil
}

func (socks5NoProgressConn) SetReadDeadline(time.Time) error {
	return nil
}

func (socks5NoProgressConn) SetWriteDeadline(time.Time) error {
	return nil
}

type socks5NoProgressReadConn struct{}

func (socks5NoProgressReadConn) Read([]byte) (int, error) {
	return 0, nil
}

func (socks5NoProgressReadConn) Write(p []byte) (int, error) {
	return len(p), nil
}

func (socks5NoProgressReadConn) Close() error {
	return nil
}

func (socks5NoProgressReadConn) LocalAddr() net.Addr {
	return socks5TestAddr("local")
}

func (socks5NoProgressReadConn) RemoteAddr() net.Addr {
	return socks5TestAddr("remote")
}

func (socks5NoProgressReadConn) SetDeadline(time.Time) error {
	return nil
}

func (socks5NoProgressReadConn) SetReadDeadline(time.Time) error {
	return nil
}

func (socks5NoProgressReadConn) SetWriteDeadline(time.Time) error {
	return nil
}

type socks5TestAddr string

func (a socks5TestAddr) Network() string { return string(a) }
func (a socks5TestAddr) String() string  { return string(a) }
