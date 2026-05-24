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

func TestProbeSidecarForModeAcceptsErrorAckAsResponsive(t *testing.T) {
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
		if req.ProfileID != sidecarProbeProfileID || req.TargetHost != sidecarProbeProfileID || req.Port != 1 {
			t.Errorf("probe request = %+v", req)
			return
		}
		writeSidecarTestFrame(t, conn, []byte(`{"ok":false,"error":"unknown profile"}`))
	}()

	err := ProbeSidecarForMode(context.Background(), "/tmp/probe-sidecar.sock", ModeMimicryClaudeCode)

	if err != nil {
		t.Fatalf("error ACK still proves sidecar responsiveness: %v", err)
	}
	<-serverDone
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
