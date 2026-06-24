package mimicry

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
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
		// forceH1=false 时控制帧不得携带 force_h1(omitempty),保持旧线缆字节。
		if req.ForceH1 != nil {
			t.Errorf("forceH1=false 时 ForceH1 应为 nil(键被省略),got %v", *req.ForceH1)
			return
		}
		writeSidecarTestFrame(t, conn, []byte(`{"ok":true}`))
		if _, err := conn.Write([]byte("plaintext")); err != nil {
			t.Errorf("write plaintext: %v", err)
		}
	}()

	client := NewSidecarClient("/tmp/tls-sidecar.sock")
	conn, err := client.DialTLS(context.Background(), "api.anthropic.com", 443, SidecarProfileAnthropicCLIMimicryV1, false)
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

	conn, err := client.DialTLS(ctx, "127.0.0.1", 443, SidecarProfileAnthropicCLIMimicryV1, false)

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

	conn, err := client.DialTLS(ctx, "api.anthropic.com", 443, SidecarProfileAnthropicCLIMimicryV1, false)

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

// TestSidecarControlRequestForceH1JSONSerialization 守护 force_h1 的线缆兼容旋钮:
//   - ForceH1=nil 时 JSON 不得含 force_h1 键(omitempty),否则改变发往老 Rust sidecar 的
//     字节,破坏向后兼容。变异证伪:去掉 json tag 的 omitempty,nil 会输出 "force_h1":null,
//     第一段断言转红。
//   - ForceH1=非 nil 时必须出现 force_h1 键并带正确布尔值,否则 Go 端开启强制 H1 后 sidecar
//     收不到意图,旋钮失效。
func TestSidecarControlRequestForceH1JSONSerialization(t *testing.T) {
	nilReq := sidecarControlRequest{
		TargetHost: "api.anthropic.com",
		Port:       443,
		ProfileID:  SidecarProfileAnthropicCLIMimicryV1,
		ForceH1:    forceH1Ptr(false),
	}
	nilJSON, err := json.Marshal(nilReq)
	if err != nil {
		t.Fatalf("marshal nil-forceH1 request: %v", err)
	}
	if strings.Contains(string(nilJSON), "force_h1") {
		t.Fatalf("forceH1=false(nil 指针)时 JSON 不得含 force_h1 键,got %s", nilJSON)
	}

	trueReq := sidecarControlRequest{
		TargetHost: "api.anthropic.com",
		Port:       443,
		ProfileID:  SidecarProfileAnthropicCLIMimicryV1,
		ForceH1:    forceH1Ptr(true),
	}
	trueJSON, err := json.Marshal(trueReq)
	if err != nil {
		t.Fatalf("marshal true-forceH1 request: %v", err)
	}
	if !strings.Contains(string(trueJSON), `"force_h1":true`) {
		t.Fatalf("forceH1=true 时 JSON 必须含 force_h1:true,got %s", trueJSON)
	}
}

// TestSidecarRoundTripperForceH1ThreadsThroughDial 守护 forceH1 选项从 RoundTripper
// 构造一路传到每次拨号的控制帧。变异证伪:把 DialTLSContext 里传给 DialTLS 的 rt.forceH1
// 改成硬编码 false,则线缆控制帧不再带 force_h1,断言转红。
func TestSidecarRoundTripperForceH1ThreadsThroughDial(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	oldDial := sidecarDialContext
	sidecarDialContext = func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	}
	defer func() { sidecarDialContext = oldDial }()

	gotForceH1 := make(chan *bool, 1)
	go func() {
		conn := serverConn
		var prefix [4]byte
		if _, err := io.ReadFull(conn, prefix[:]); err != nil {
			t.Errorf("read prefix: %v", err)
			gotForceH1 <- nil
			return
		}
		body := make([]byte, binary.LittleEndian.Uint32(prefix[:]))
		if _, err := io.ReadFull(conn, body); err != nil {
			t.Errorf("read body: %v", err)
			gotForceH1 <- nil
			return
		}
		var req sidecarControlRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decode request: %v", err)
			gotForceH1 <- nil
			return
		}
		gotForceH1 <- req.ForceH1
		writeSidecarTestFrame(t, conn, []byte(`{"ok":true}`))
	}()

	rt := &sidecarRoundTripper{
		client:    NewSidecarClient("/tmp/force-h1-thread.sock"),
		profileID: SidecarProfileAnthropicCLIMimicryV1,
		forceH1:   true,
	}
	conn, err := rt.DialTLSContext(context.Background(), "tcp", "api.anthropic.com:443")
	if err != nil {
		t.Fatalf("DialTLSContext: %v", err)
	}
	defer conn.Close()

	forceH1 := <-gotForceH1
	if forceH1 == nil {
		t.Fatal("forceH1=true 的 RoundTripper 必须让控制帧带 force_h1,got nil(键缺失)")
	}
	if !*forceH1 {
		t.Fatalf("force_h1 值应为 true,got %v", *forceH1)
	}
}

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
