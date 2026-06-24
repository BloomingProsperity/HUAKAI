package mimicry

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
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

// SidecarClient 把 Go 传输链路接到本机 BoringSSL TLS sidecar。
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
		MaxIdleConns:        256,
		MaxIdleConnsPerHost: 64, // DM-17:默认 2 在网关负载下复用近失效
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
}

// DialTLS 拨本机 Unix sidecar,发送一帧 JSON 控制消息,等待 ACK 帧,然后返回
// sidecar 持有 TLS 连接之上的明文流。sidecar 失败必须 fail-closed,本函数
// 绝不回退到 uTLS 或标准库传输。
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
	if err := clearDeadline(conn); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

type sidecarRoundTripper struct {
	client    *SidecarClient
	profileID string
}

func (rt *sidecarRoundTripper) DialTLSContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if rt == nil {
		return nil, fmt.Errorf("mimicry sidecar: nil round tripper")
	}
	if rt.client == nil {
		return nil, fmt.Errorf("mimicry sidecar: nil client")
	}
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
	if _, err := writeFullConn(conn, prefix[:]); err != nil {
		return err
	}
	_, err = writeFullConn(conn, body)
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
		if n == 0 {
			return read, io.ErrNoProgress
		}
	}
	return read, nil
}

func writeFullConn(conn net.Conn, buf []byte) (int, error) {
	written := 0
	for written < len(buf) {
		n, err := conn.Write(buf[written:])
		written += n
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

func clearDeadline(conn net.Conn) error {
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return fmt.Errorf("mimicry sidecar: clear deadline: %w", err)
	}
	return nil
}

func setDeadlineFromContext(conn net.Conn, ctx context.Context) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("mimicry sidecar: set deadline: %w", err)
	}
	return nil
}
