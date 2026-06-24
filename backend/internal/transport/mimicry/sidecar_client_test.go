package mimicry

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSidecarClientDialTLSWritesLittleEndianControlAndReturnsPlaintextConn(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	oldDial := sidecarDialContext
	sidecarDialContext = func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	}
	defer func() { sidecarDialContext = oldDial }()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn := serverConn
		var prefix [4]byte
		if _, err := io.ReadFull(conn, prefix[:]); err != nil {
			t.Errorf("read prefix: %v", err)
			return
		}
		n := binary.LittleEndian.Uint32(prefix[:])
		if n == binary.BigEndian.Uint32(prefix[:]) {
			t.Errorf("fixture must distinguish little endian from big endian")
			return
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(conn, body); err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		var req sidecarControlRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if req.TargetHost != "api.anthropic.com" || req.Port != 443 || req.ProfileID != SidecarProfileAnthropicCLIMimicryV1 {
			t.Errorf("control request = %+v", req)
			return
		}
		writeSidecarTestFrame(t, conn, []byte(`{"ok":true}`))
		if _, err := conn.Write([]byte("plaintext")); err != nil {
			t.Errorf("write plaintext: %v", err)
		}
	}()

	client := NewSidecarClient("/tmp/tls-sidecar.sock")
	conn, err := client.DialTLS(context.Background(), "api.anthropic.com", 443, SidecarProfileAnthropicCLIMimicryV1)
	if err != nil {
		t.Fatalf("DialTLS: %v", err)
	}
	defer conn.Close()
	buf := make([]byte, len("plaintext"))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read plaintext: %v", err)
	}
	if string(buf) != "plaintext" {
		t.Fatalf("plaintext = %q", buf)
	}
	<-serverDone
}

func TestSidecarClientDialTLSClearDeadlineFailureClosesConn(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	wrapped := &clearDeadlineFailConn{Conn: clientConn}
	oldDial := sidecarDialContext
	sidecarDialContext = func(context.Context, string, string) (net.Conn, error) {
		return wrapped, nil
	}
	defer func() { sidecarDialContext = oldDial }()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn := serverConn
		var prefix [4]byte
		if _, err := io.ReadFull(conn, prefix[:]); err != nil {
			t.Errorf("read prefix: %v", err)
			return
		}
		body := make([]byte, binary.LittleEndian.Uint32(prefix[:]))
		if _, err := io.ReadFull(conn, body); err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		writeSidecarTestFrame(t, conn, []byte(`{"ok":true}`))
	}()

	client := NewSidecarClient("/tmp/tls-sidecar.sock")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	conn, err := client.DialTLS(ctx, "api.anthropic.com", 443, SidecarProfileAnthropicCLIMimicryV1)

	if err == nil {
		conn.Close()
		t.Fatal("清理 deadline 失败时 DialTLS 应返回错误")
	}
	if !strings.Contains(err.Error(), "clear deadline") {
		t.Fatalf("错误应标明 clear deadline, got %v", err)
	}
	if wrapped.setDeadlineCalls < 2 {
		t.Fatalf("测试未走到清理 deadline 分支,setDeadlineCalls=%d", wrapped.setDeadlineCalls)
	}
	if wrapped.closeCalls != 1 {
		t.Fatalf("清理 deadline 失败应关闭连接,closeCalls=%d", wrapped.closeCalls)
	}
	<-serverDone
}

func TestSidecarClientDialTLSSetDeadlineFailureClosesOnce(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	wrapped := &setDeadlineFailConn{Conn: clientConn}
	oldDial := sidecarDialContext
	sidecarDialContext = func(context.Context, string, string) (net.Conn, error) {
		return wrapped, nil
	}
	defer func() { sidecarDialContext = oldDial }()

	client := NewSidecarClient("/tmp/tls-sidecar.sock")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	conn, err := client.DialTLS(ctx, "api.anthropic.com", 443, SidecarProfileAnthropicCLIMimicryV1)

	if err == nil {
		conn.Close()
		t.Fatal("设置 deadline 失败时 DialTLS 应返回错误")
	}
	if !strings.Contains(err.Error(), "set deadline") {
		t.Fatalf("错误应标明 set deadline, got %v", err)
	}
	if wrapped.setDeadlineCalls != 1 {
		t.Fatalf("初始 deadline 失败应只设置一次,setDeadlineCalls=%d", wrapped.setDeadlineCalls)
	}
	if wrapped.closeCalls != 1 {
		t.Fatalf("初始 deadline 失败应只关闭一次,closeCalls=%d", wrapped.closeCalls)
	}
}

func TestSidecarRoundTripperDialTLSContextRejectsNilReceiverAndClient(t *testing.T) {
	var nilRT *sidecarRoundTripper
	_, err := nilRT.DialTLSContext(context.Background(), "tcp", "api.anthropic.com:443")
	if err == nil || !strings.Contains(err.Error(), "nil round tripper") {
		t.Fatalf("nil receiver 错误=%v,want nil round tripper", err)
	}

	rt := &sidecarRoundTripper{profileID: SidecarProfileAnthropicCLIMimicryV1}
	_, err = rt.DialTLSContext(context.Background(), "tcp", "api.anthropic.com:443")
	if err == nil || !strings.Contains(err.Error(), "nil client") {
		t.Fatalf("nil client 错误=%v,want nil client", err)
	}
}

func TestSidecarClientDialTLSBadSocketFailsClosedWithoutTargetFallback(t *testing.T) {
	dialCalls := make([]string, 0, 1)
	oldDial := sidecarDialContext
	sidecarDialContext = func(_ context.Context, network, address string) (net.Conn, error) {
		dialCalls = append(dialCalls, network+" "+address)
		return nil, errors.New("missing sidecar socket")
	}
	defer func() { sidecarDialContext = oldDial }()
	client := NewSidecarClient("/tmp/missing-sidecar.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	conn, err := client.DialTLS(ctx, "127.0.0.1", 443, SidecarProfileAnthropicCLIMimicryV1)

	if err == nil {
		conn.Close()
		t.Fatal("DialTLS with missing socket returned nil error; must fail closed")
	}
	if !strings.Contains(err.Error(), "sidecar") {
		t.Fatalf("error should identify sidecar failure, got %v", err)
	}
	if len(dialCalls) != 1 || dialCalls[0] != "unix /tmp/missing-sidecar.sock" {
		t.Fatalf("bad sidecar socket should only dial sidecar once, got calls %v", dialCalls)
	}
}

func TestSidecarClientDialTLSNoAckTimesOutFailClosed(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	oldDial := sidecarDialContext
	sidecarDialContext = func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	}
	defer func() { sidecarDialContext = oldDial }()
	requestRead := make(chan struct{})
	releaseServer := make(chan struct{})
	go func() {
		defer close(requestRead)
		conn := serverConn
		var prefix [4]byte
		if _, err := io.ReadFull(conn, prefix[:]); err != nil {
			t.Errorf("read prefix: %v", err)
			return
		}
		body := make([]byte, binary.LittleEndian.Uint32(prefix[:]))
		if _, err := io.ReadFull(conn, body); err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		<-releaseServer
	}()
	client := NewSidecarClient("/tmp/no-ack-sidecar.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	conn, err := client.DialTLS(ctx, "api.anthropic.com", 443, SidecarProfileAnthropicCLIMimicryV1)

	close(releaseServer)
	if err == nil {
		conn.Close()
		t.Fatal("DialTLS with nonresponsive sidecar returned nil error; must fail closed")
	}
	<-requestRead
	if !strings.Contains(err.Error(), "read ack frame") {
		t.Fatalf("error should identify missing sidecar ack, got %v", err)
	}
}

func TestProbeSidecarForModeClassifiesUnknownProfileAck(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	oldDial := sidecarDialContext
	sidecarDialContext = func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	}
	defer func() { sidecarDialContext = oldDial }()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn := serverConn
		var prefix [4]byte
		if _, err := io.ReadFull(conn, prefix[:]); err != nil {
			t.Errorf("read prefix: %v", err)
			return
		}
		body := make([]byte, binary.LittleEndian.Uint32(prefix[:]))
		if _, err := io.ReadFull(conn, body); err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		var req sidecarControlRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if req.ProfileID != SidecarProfileAnthropicCLIMimicryV1 || req.TargetHost != sidecarProbeProfileID || req.Port != 1 {
			t.Errorf("probe request = %+v", req)
			return
		}
		writeSidecarTestFrame(t, conn, []byte(`{"ok":false,"error":"unknown profile"}`))
	}()

	err := ProbeSidecarForMode(context.Background(), "/tmp/probe-sidecar.sock", ModeMimicryClaudeCode)

	if !errors.Is(err, ErrSidecarProfileUnavailable) {
		t.Fatalf("profile error ACK must be classified as profile unavailable, got %v", err)
	}
	<-serverDone
}

func TestProbeSidecarForModeAcceptsNonProfileErrorAck(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	oldDial := sidecarDialContext
	sidecarDialContext = func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	}
	defer func() { sidecarDialContext = oldDial }()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn := serverConn
		var prefix [4]byte
		if _, err := io.ReadFull(conn, prefix[:]); err != nil {
			t.Errorf("read prefix: %v", err)
			return
		}
		body := make([]byte, binary.LittleEndian.Uint32(prefix[:]))
		if _, err := io.ReadFull(conn, body); err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		var req sidecarControlRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if req.ProfileID != SidecarProfileAnthropicCLIMimicryV1 || req.TargetHost != sidecarProbeProfileID || req.Port != 1 {
			t.Errorf("probe request = %+v", req)
			return
		}
		writeSidecarTestFrame(t, conn, []byte(`{"ok":false,"error":"upstream tcp error: failed to lookup address information"}`))
	}()

	err := ProbeSidecarForMode(context.Background(), "/tmp/probe-sidecar.sock", ModeMimicryClaudeCode)

	if err != nil {
		t.Fatalf("non-profile error ACK proves sidecar liveness and profile lookup, got %v", err)
	}
	<-serverDone
}

func TestWriteSidecarFrameHandlesShortWrites(t *testing.T) {
	conn := &shortWriteConn{maxChunk: 2}
	req := sidecarControlRequest{
		TargetHost: "api.anthropic.com",
		Port:       443,
		ProfileID:  SidecarProfileAnthropicCLIMimicryV1,
	}

	if err := writeSidecarFrame(conn, req); err != nil {
		t.Fatalf("writeSidecarFrame: %v", err)
	}
	if conn.writeCalls < 4 {
		t.Fatalf("fixture lost discrimination: writeCalls=%d want multiple short writes", conn.writeCalls)
	}
	if len(conn.buf) < 4 {
		t.Fatalf("written frame too short: %d", len(conn.buf))
	}
	n := binary.LittleEndian.Uint32(conn.buf[:4])
	body := conn.buf[4:]
	if int(n) != len(body) {
		t.Fatalf("frame length prefix=%d body len=%d", n, len(body))
	}
	var got sidecarControlRequest
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode frame body: %v", err)
	}
	if got != req {
		t.Fatalf("request round trip=%+v want %+v", got, req)
	}
}

func TestReadSidecarFrameRejectsNoProgressRead(t *testing.T) {
	conn := noProgressConn{}
	var ack sidecarControlAck
	err := readSidecarFrame(conn, &ack)
	if !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("readSidecarFrame 零进展读错误=%v,want io.ErrNoProgress", err)
	}
}

func TestWriteSidecarFrameRejectsNoProgressWrite(t *testing.T) {
	conn := noProgressConn{}
	err := writeSidecarFrame(conn, sidecarControlRequest{
		TargetHost: "api.anthropic.com",
		Port:       443,
		ProfileID:  SidecarProfileAnthropicCLIMimicryV1,
	})
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("writeSidecarFrame 零进展写错误=%v,want io.ErrShortWrite", err)
	}
}

type noProgressConn struct{}

func (noProgressConn) Read([]byte) (int, error) {
	return 0, nil
}

func (noProgressConn) Write([]byte) (int, error) {
	return 0, nil
}

func (noProgressConn) Close() error {
	return nil
}

func (noProgressConn) LocalAddr() net.Addr {
	return dummyAddr("local")
}

func (noProgressConn) RemoteAddr() net.Addr {
	return dummyAddr("remote")
}

func (noProgressConn) SetDeadline(time.Time) error {
	return nil
}

func (noProgressConn) SetReadDeadline(time.Time) error {
	return nil
}

func (noProgressConn) SetWriteDeadline(time.Time) error {
	return nil
}

type clearDeadlineFailConn struct {
	net.Conn
	mu               sync.Mutex
	setDeadlineCalls int
	closeCalls       int
}

func (c *clearDeadlineFailConn) SetDeadline(deadline time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setDeadlineCalls++
	if deadline.IsZero() {
		return errors.New("clear deadline failed")
	}
	return c.Conn.SetDeadline(deadline)
}

func (c *clearDeadlineFailConn) Close() error {
	c.mu.Lock()
	c.closeCalls++
	c.mu.Unlock()
	return c.Conn.Close()
}

type setDeadlineFailConn struct {
	net.Conn
	mu               sync.Mutex
	setDeadlineCalls int
	closeCalls       int
}

func (c *setDeadlineFailConn) SetDeadline(time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setDeadlineCalls++
	return errors.New("set deadline failed")
}

func (c *setDeadlineFailConn) Close() error {
	c.mu.Lock()
	c.closeCalls++
	c.mu.Unlock()
	return c.Conn.Close()
}

type shortWriteConn struct {
	mu         sync.Mutex
	buf        []byte
	maxChunk   int
	writeCalls int
}

func (c *shortWriteConn) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (c *shortWriteConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := c.maxChunk
	if n <= 0 || n > len(p) {
		n = len(p)
	}
	c.buf = append(c.buf, p[:n]...)
	c.writeCalls++
	return n, nil
}

func (c *shortWriteConn) Close() error {
	return nil
}

func (c *shortWriteConn) LocalAddr() net.Addr {
	return dummyAddr("local")
}

func (c *shortWriteConn) RemoteAddr() net.Addr {
	return dummyAddr("remote")
}

func (c *shortWriteConn) SetDeadline(time.Time) error {
	return nil
}

func (c *shortWriteConn) SetReadDeadline(time.Time) error {
	return nil
}

func (c *shortWriteConn) SetWriteDeadline(time.Time) error {
	return nil
}

type dummyAddr string

func (a dummyAddr) Network() string { return string(a) }
func (a dummyAddr) String() string  { return string(a) }

func writeSidecarTestFrame(t *testing.T, conn net.Conn, body []byte) {
	t.Helper()
	var prefix [4]byte
	binary.LittleEndian.PutUint32(prefix[:], uint32(len(body)))
	if _, err := conn.Write(prefix[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(body); err != nil {
		t.Fatal(err)
	}
}
