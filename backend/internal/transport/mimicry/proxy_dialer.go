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
	"strings"
	"time"
)

// ProxyDialerFunc 在 uTLS 握手【之下】建立到目标的原始 TCP 连接 —— 即先经代理
// 拨到 target,再在返回的 conn 上跑自定义 ClientHello。这样出口 IP 是代理的,
// JA3 仍是伪装指纹,二者得以共存(PROXY-02a)。
type ProxyDialerFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// proxyDialerFromURL 按代理 URL 构造 ProxyDialerFunc。
//
// 支持 http/https CONNECT 代理(住宅/数据中心代理的主流形态)。socks5 暂不在
// 伪装路支持 —— 返回 error 让 dispatch【fail-loud】,绝不回退直连(否则真实出口
// IP 会泄露,破坏账号级 IP 隔离 + 反封禁)。
func proxyDialerFromURL(proxyURL *url.URL) (ProxyDialerFunc, error) {
	if proxyURL == nil {
		return nil, fmt.Errorf("mimicry proxy: nil proxy url")
	}
	switch strings.ToLower(proxyURL.Scheme) {
	case "http", "https":
		return httpConnectDialer(proxyURL), nil
	default:
		return nil, fmt.Errorf("mimicry proxy: 暂不支持的代理 scheme %q(伪装路仅支持 http/https CONNECT)", proxyURL.Scheme)
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
		d := &net.Dialer{Timeout: 30 * time.Second}
		proxyConn, err := d.DialContext(ctx, "tcp", proxyHostPort(proxyURL))
		if err != nil {
			return nil, fmt.Errorf("mimicry proxy: 拨号代理 %s 失败: %w", proxyURL.Host, err)
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
		if deadline, ok := ctx.Deadline(); ok {
			_ = proxyConn.SetDeadline(deadline)
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
		_ = proxyConn.SetDeadline(time.Time{})
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
