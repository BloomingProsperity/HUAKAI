package mimicry

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ProxyDialerFunc 在 uTLS 握手【之下】建立到目标的原始 TCP 连接 —— 即先经代理
// 拨到 target,再在返回的 conn 上跑自定义 ClientHello。这样出口 IP 是代理的,
// JA3 仍是伪装指纹,二者得以共存(PROXY-02a)。
type ProxyDialerFunc func(ctx context.Context, network, addr string) (net.Conn, error)

var proxyDialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 30 * time.Second}
	return d.DialContext(ctx, network, addr)
}

// proxyDialerFromURL 按代理 URL 构造 ProxyDialerFunc。
//
// 支持 http/https CONNECT 与 socks5/socks5h。未知 scheme 必须 fail-loud,
// 绝不回退直连(否则真实出口 IP 会泄露,破坏账号级 IP 隔离 + 反封禁)。
func proxyDialerFromURL(proxyURL *url.URL) (ProxyDialerFunc, error) {
	if proxyURL == nil {
		return nil, fmt.Errorf("mimicry proxy: nil proxy url")
	}
	switch strings.ToLower(proxyURL.Scheme) {
	case "http", "https":
		return httpConnectDialer(proxyURL), nil
	case "socks5", "socks5h":
		return socks5Dialer(proxyURL), nil
	default:
		return nil, fmt.Errorf("mimicry proxy: 暂不支持的代理 scheme %q(伪装路仅支持 http/https CONNECT 与 socks5/socks5h)", proxyURL.Scheme)
	}
}

// proxyHostPort 返回代理的 host:port,按 scheme 补默认端口。
func proxyHostPort(proxyURL *url.URL) string {
	host := proxyURL.Hostname()
	port := proxyURL.Port()
	if port == "" {
		if strings.EqualFold(proxyURL.Scheme, "https") {
			port = "443"
		} else {
			port = "80"
		}
	}
	return net.JoinHostPort(host, port)
}

// httpConnectDialer 返回一个经 HTTP(S) CONNECT 代理拨号的 ProxyDialerFunc。
func httpConnectDialer(proxyURL *url.URL) ProxyDialerFunc {
	return func(ctx context.Context, _, addr string) (net.Conn, error) {
		proxyConn, err := proxyDialContext(ctx, "tcp", proxyHostPort(proxyURL))
		if err != nil {
			return nil, fmt.Errorf("mimicry proxy: 拨号代理 %s 失败: %w", RedactProxyURL(proxyURL), err)
		}
		// https 代理:先与代理本身完成 TLS,再发 CONNECT。
		if strings.EqualFold(proxyURL.Scheme, "https") {
			tconn := tls.Client(proxyConn, &tls.Config{ServerName: proxyURL.Hostname()})
			if err := tconn.HandshakeContext(ctx); err != nil {
				proxyConn.Close()
				return nil, fmt.Errorf("mimicry proxy: 与代理 TLS 握手失败: %w", err)
			}
			proxyConn = tconn
		}
		if err := setProxyDeadlineFromContext(proxyConn, ctx); err != nil {
			proxyConn.Close()
			return nil, err
		}
		connectReq := &http.Request{
			Method: http.MethodConnect,
			URL:    &url.URL{Opaque: addr},
			Host:   addr,
			Header: make(http.Header),
		}
		if u := proxyURL.User; u != nil {
			pwd, _ := u.Password()
			creds := base64.StdEncoding.EncodeToString([]byte(u.Username() + ":" + pwd))
			connectReq.Header.Set("Proxy-Authorization", "Basic "+creds)
		}
		if err := connectReq.Write(proxyConn); err != nil {
			proxyConn.Close()
			return nil, fmt.Errorf("mimicry proxy: 写 CONNECT 失败: %w", err)
		}
		br := bufio.NewReader(proxyConn)
		resp, err := http.ReadResponse(br, connectReq)
		if err != nil {
			proxyConn.Close()
			return nil, fmt.Errorf("mimicry proxy: 读 CONNECT 响应失败: %w", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			proxyConn.Close()
			return nil, fmt.Errorf("mimicry proxy: CONNECT %s 被拒: %s", addr, resp.Status)
		}
		// 清除拨号 deadline,后续 uTLS 握手/读写自管。
		if err := clearProxyDeadline(proxyConn); err != nil {
			proxyConn.Close()
			return nil, err
		}
		// CONNECT 响应无 body;若 bufio 预读了多余字节(隧道早期数据),用
		// bufferedConn 兜住,避免丢失。
		if br.Buffered() > 0 {
			return &bufferedConn{Conn: proxyConn, r: br}, nil
		}
		return proxyConn, nil
	}
}

// bufferedConn 把一个已预读若干字节的 bufio.Reader 与底层 conn 重新粘合。
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// socks5Dialer 返回经 SOCKS5 代理拨号的 ProxyDialerFunc(手写握手,不引 x/net)。
// 用 domain atyp(0x03)让代理端解析目标(socks5h 语义),适配住宅代理出口。
func socks5Dialer(proxyURL *url.URL) ProxyDialerFunc {
	return func(ctx context.Context, _, addr string) (net.Conn, error) {
		host, portStr, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("mimicry proxy: socks5 target %q: %w", addr, err)
		}
		port, err := strconv.Atoi(portStr)
		if err != nil || port <= 0 || port > 65535 {
			return nil, fmt.Errorf("mimicry proxy: socks5 bad port %q", portStr)
		}
		conn, err := proxyDialContext(ctx, "tcp", proxyURL.Host)
		if err != nil {
			return nil, fmt.Errorf("mimicry proxy: 拨号 socks5 %s 失败: %w", RedactProxyURL(proxyURL), err)
		}
		if err := setProxyDeadlineFromContext(conn, ctx); err != nil {
			conn.Close()
			return nil, err
		}
		if err := socks5Handshake(conn, proxyURL.User, host, port); err != nil {
			conn.Close()
			return nil, err
		}
		if err := clearProxyDeadline(conn); err != nil {
			conn.Close()
			return nil, err
		}
		return conn, nil
	}
}

func setProxyDeadlineFromContext(conn net.Conn, ctx context.Context) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("mimicry proxy: set deadline: %w", err)
	}
	return nil
}

func clearProxyDeadline(conn net.Conn) error {
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return fmt.Errorf("mimicry proxy: clear deadline: %w", err)
	}
	return nil
}

// socks5Handshake 跑 SOCKS5 客户端握手(方法协商 + 可选 user/pass 认证 +
// CONNECT),成功后 conn 即到目标的隧道。
func socks5Handshake(conn net.Conn, user *url.Userinfo, host string, port int) error {
	hasAuth := user != nil && user.Username() != ""
	if hasAuth {
		if err := writeFullConn(conn, []byte{0x05, 0x02, 0x00, 0x02}); err != nil {
			return fmt.Errorf("socks5: write methods: %w", err)
		}
	} else {
		if err := writeFullConn(conn, []byte{0x05, 0x01, 0x00}); err != nil {
			return fmt.Errorf("socks5: write methods: %w", err)
		}
	}
	sel := make([]byte, 2)
	if err := readFullConn(conn, sel); err != nil {
		return fmt.Errorf("socks5: read method: %w", err)
	}
	if sel[0] != 0x05 {
		return fmt.Errorf("socks5: bad version %d", sel[0])
	}
	switch sel[1] {
	case 0x00:
		// 无需认证。
	case 0x02:
		if !hasAuth {
			return fmt.Errorf("socks5: server demands auth but no credentials")
		}
		u := user.Username()
		pw, _ := user.Password()
		if len(u) > 255 || len(pw) > 255 {
			return fmt.Errorf("socks5: credential too long")
		}
		buf := []byte{0x01, byte(len(u))}
		buf = append(buf, u...)
		buf = append(buf, byte(len(pw)))
		buf = append(buf, pw...)
		if err := writeFullConn(conn, buf); err != nil {
			return fmt.Errorf("socks5: write auth: %w", err)
		}
		ar := make([]byte, 2)
		if err := readFullConn(conn, ar); err != nil {
			return fmt.Errorf("socks5: read auth reply: %w", err)
		}
		if ar[1] != 0x00 {
			return fmt.Errorf("socks5: auth failed (status=%d)", ar[1])
		}
	default:
		return fmt.Errorf("socks5: unacceptable method %d", sel[1])
	}
	if len(host) > 255 {
		return fmt.Errorf("socks5: host too long")
	}
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))}
	req = append(req, host...)
	req = append(req, byte(port>>8), byte(port&0xff))
	if err := writeFullConn(conn, req); err != nil {
		return fmt.Errorf("socks5: write connect: %w", err)
	}
	head := make([]byte, 4)
	if err := readFullConn(conn, head); err != nil {
		return fmt.Errorf("socks5: read connect reply: %w", err)
	}
	if head[1] != 0x00 {
		return fmt.Errorf("socks5: CONNECT rejected (rep=%d)", head[1])
	}
	var addrLen int
	switch head[3] {
	case 0x01:
		addrLen = 4
	case 0x04:
		addrLen = 16
	case 0x03:
		l := make([]byte, 1)
		if err := readFullConn(conn, l); err != nil {
			return fmt.Errorf("socks5: read bnd len: %w", err)
		}
		addrLen = int(l[0])
	default:
		return fmt.Errorf("socks5: bad reply atyp %d", head[3])
	}
	if err := readFullConn(conn, make([]byte, addrLen+2)); err != nil {
		return fmt.Errorf("socks5: read bind addr: %w", err)
	}
	return nil
}

// RedactProxyURL 返回代理 URL 的日志安全形态:scheme://host,带凭据时 user 段
// 替成 redacted、丢弃 password/path/query(PROXYHDR-01)。任何打印代理 URL 的
// 地方都该走它,杜绝 user:pass@host basic-auth 泄进日志。
func RedactProxyURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	if u.User != nil && u.User.Username() != "" {
		return u.Scheme + "://redacted@" + u.Host
	}
	return u.Scheme + "://" + u.Host
}
