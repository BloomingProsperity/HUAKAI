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
	return NewSidecarRoundTripperForceH1(client, profileID, forceH1Enabled())
}

// NewSidecarRoundTripperForceH1 在 NewSidecarRoundTripper 基础上显式指定 forceH1。
// forceH1=true 时,每次拨号的 control request 携带 force_h1,令 Rust sidecar 握手只广告
// ALPN=http/1.1,从根消除 h2 升级(与 Go uTLS 路 utls_dialer.go 的 ForceH1 姿态一致)。
func NewSidecarRoundTripperForceH1(client *SidecarClient, profileID string, forceH1 bool) http.RoundTripper {
	rt := &sidecarRoundTripper{client: client, profileID: profileID, forceH1: forceH1}
	return &http.Transport{
		DialTLSContext:      rt.DialTLSContext,
		ForceAttemptHTTP2:   false,
		DisableCompression:  false,
		MaxIdleConns:        256,
		MaxIdleConnsPerHost: 64, // DM-17:默认 2 在网关负载下复用近失效
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
}

// DialTLS dials the Unix sidecar, sends one framed JSON control message, waits
// for an ACK frame, then returns a plaintext stream over the sidecar-owned TLS
// connection. Sidecar failure is fail-closed; this function never falls back to
// uTLS or the standard transport.
func (c *SidecarClient) DialTLS(ctx context.Context, host string, port int, profileID string, forceH1 bool) (net.Conn, error) {
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
		ForceH1:    forceH1Ptr(forceH1),
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
	// forceH1 为 true 时,每次 DialTLS 都让 sidecar 握手只广告 ALPN=http/1.1。
	forceH1 bool
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
	return rt.client.DialTLS(ctx, host, port, rt.profileID, rt.forceH1)
}

type sidecarControlRequest struct {
	TargetHost string `json:"target_host"`
	Port       uint16 `json:"port"`
	ProfileID  string `json:"profile_id"`
	// ForceH1 仅在非 nil 时随 control frame 下发(omitempty + 指针);nil(默认,旧线缆)时
	// JSON 不含 force_h1 键,与历史 Rust sidecar 完全兼容。Rust 侧 serde(default) 把缺省解为 None。
	ForceH1 *bool `json:"force_h1,omitempty"`
}

// forceH1Ptr 把 per-dial 的 forceH1 决策转成可省略的 *bool:false 时返回 nil(线缆不带该键,
// = 旧行为),true 时返回指向 true 的指针(线缆显式带 force_h1:true)。这样默认关闭路径
// 不改变发往老 sidecar 的字节,满足向后兼容。
func forceH1Ptr(forceH1 bool) *bool {
	if !forceH1 {
		return nil
	}
	return &forceH1
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
