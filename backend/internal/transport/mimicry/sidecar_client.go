package mimicry

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"
)

const (
	SidecarProfileAnthropicCLIMimicryV1 = "anthropic-cli-mimicry-v1"
	sidecarMaxFrameLen                  = 1024 * 1024
)

var sidecarDialContext = (&net.Dialer{}).DialContext

// SidecarClient connects Go's transport path to the local BoringSSL TLS sidecar.
type SidecarClient struct {
	socketPath string
}

func NewSidecarClient(socketPath string) *SidecarClient {
	return &SidecarClient{socketPath: socketPath}
}

func NewSidecarRoundTripper(client *SidecarClient, profileID string) http.RoundTripper {
	rt := &sidecarRoundTripper{client: client, profileID: profileID}
	return &http.Transport{
		DialTLSContext:      rt.DialTLSContext,
		ForceAttemptHTTP2:   false,
		DisableCompression:  false,
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
}

// DialTLS dials the Unix sidecar, sends one framed JSON control message, waits
// for an ACK frame, then returns a plaintext stream over the sidecar-owned TLS
// connection. Sidecar failure is fail-closed; this function never falls back to
// uTLS or the standard transport.
func (c *SidecarClient) DialTLS(ctx context.Context, host string, port int, profileID string) (net.Conn, error) {
	if c == nil {
		return nil, fmt.Errorf("mimicry sidecar: nil client")
	}
	if c.socketPath == "" {
		return nil, fmt.Errorf("mimicry sidecar: empty socket path")
	}
	if host == "" || port <= 0 || port > 65535 {
		return nil, fmt.Errorf("mimicry sidecar: invalid target %q:%d", host, port)
	}
	if profileID == "" {
		return nil, fmt.Errorf("mimicry sidecar: empty profile id")
	}
	conn, err := sidecarDialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return nil, fmt.Errorf("mimicry sidecar: dial unix socket %s: %w", c.socketPath, err)
	}
	if err := setDeadlineFromContext(conn, ctx); err != nil {
		conn.Close()
		return nil, err
	}
	req := sidecarControlRequest{
		TargetHost: host,
		Port:       uint16(port),
		ProfileID:  profileID,
	}
	if err := writeSidecarFrame(conn, req); err != nil {
		conn.Close()
		return nil, fmt.Errorf("mimicry sidecar: write control frame: %w", err)
	}
	var ack sidecarControlAck
	if err := readSidecarFrame(conn, &ack); err != nil {
		conn.Close()
		return nil, fmt.Errorf("mimicry sidecar: read ack frame: %w", err)
	}
	if !ack.OK {
		conn.Close()
		if ack.Error == "" {
			ack.Error = "sidecar rejected request"
		}
		return nil, fmt.Errorf("mimicry sidecar: %s", ack.Error)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

type sidecarRoundTripper struct {
	client    *SidecarClient
	profileID string
}

func (rt *sidecarRoundTripper) DialTLSContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("mimicry sidecar: unsupported network %s", network)
	}
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("mimicry sidecar: split target address %q: %w", addr, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return nil, fmt.Errorf("mimicry sidecar: parse target port %q: %w", portText, err)
	}
	return rt.client.DialTLS(ctx, host, port, rt.profileID)
}

type sidecarControlRequest struct {
	TargetHost string `json:"target_host"`
	Port       uint16 `json:"port"`
	ProfileID  string `json:"profile_id"`
}

type sidecarControlAck struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func writeSidecarFrame(conn net.Conn, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(body) > sidecarMaxFrameLen {
		return fmt.Errorf("frame length %d exceeds max %d", len(body), sidecarMaxFrameLen)
	}
	var prefix [4]byte
	binary.LittleEndian.PutUint32(prefix[:], uint32(len(body)))
	if _, err := conn.Write(prefix[:]); err != nil {
		return err
	}
	_, err = conn.Write(body)
	return err
}

func readSidecarFrame(conn net.Conn, value any) error {
	var prefix [4]byte
	if _, err := readFullConn(conn, prefix[:]); err != nil {
		return err
	}
	n := binary.LittleEndian.Uint32(prefix[:])
	if n > sidecarMaxFrameLen {
		return fmt.Errorf("frame length %d exceeds max %d", n, sidecarMaxFrameLen)
	}
	body := make([]byte, n)
	if _, err := readFullConn(conn, body); err != nil {
		return err
	}
	return json.Unmarshal(body, value)
}

func readFullConn(conn net.Conn, buf []byte) (int, error) {
	read := 0
	for read < len(buf) {
		n, err := conn.Read(buf[read:])
		read += n
		if err != nil {
			return read, err
		}
	}
	return read, nil
}

func setDeadlineFromContext(conn net.Conn, ctx context.Context) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil
	}
	if err := conn.SetDeadline(deadline); err != nil {
		conn.Close()
		return fmt.Errorf("mimicry sidecar: set deadline: %w", err)
	}
	return nil
}
