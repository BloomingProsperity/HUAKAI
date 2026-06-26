package proxyhealth

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/ssrfpolicy"
)

// startConnectProxy 起一个进程内最小 HTTP CONNECT 代理,把 CONNECT 隧道转发到目标。
// 用于验证 probe 经"真代理"建隧道的全链路(不依赖外网)。
func startConnectProxy(t *testing.T) *url.URL {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(client net.Conn) {
				defer client.Close()
				br := bufio.NewReader(client)
				req, err := http.ReadRequest(br)
				if err != nil || req.Method != http.MethodConnect {
					return
				}
				target, err := net.Dial("tcp", req.Host)
				if err != nil {
					_, _ = client.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
					return
				}
				defer target.Close()
				_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
				go func() { _, _ = io.Copy(target, br) }()
				_, _ = io.Copy(client, target)
			}(c)
		}
	}()
	return &url.URL{Scheme: "http", Host: ln.Addr().String()}
}

// SSRF 守卫②:canary 未过 SSRF 策略 → target_denied,且**绝不拨号**(dialer 都不构造)。
// 变异:删 ProbeThrough 里 policy.AllowsHost 复校 → 会去拨 proxyURL(本例代理是坏的)得别的 error_class → 红。
func TestProbeThroughDeniesNonAllowlistedCanary(t *testing.T) {
	policy, err := ssrfpolicy.Parse("", "blocked.example", "", "", "")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	res := ProbeThrough(context.Background(), policy, &url.URL{Scheme: "http", Host: "10.0.0.1:8080"}, "blocked.example:443")
	if res.OK || res.ErrorClass != ErrClassTargetDenied {
		t.Fatalf("被拒 canary 必须 target_denied 且 ok=false,实得 %+v", res)
	}
}

func TestProbeThroughBadProxyURL(t *testing.T) {
	res := ProbeThrough(context.Background(), ssrfpolicy.Policy{}, &url.URL{Scheme: "ftp", Host: "h:1"}, "example.com:443")
	if res.OK || res.ErrorClass != ErrClassBadProxyURL {
		t.Fatalf("不支持的 scheme 必须 bad_proxy_url,实得 %+v", res)
	}
}

// 隧道真达 canary:经进程内 CONNECT 代理建隧道到一个纯 HTTP(非 TLS)canary,TLS 握手必败 →
// tls_fail(而非 tunnel_refused),证明隧道 + 到目标的连接 + TLS 尝试全链路通,只差对端非 TLS。
// 同时断言结果**不含任何代理 URL/凭据**。
func TestProbeThroughTunnelReachesCanaryThenTLSFails(t *testing.T) {
	canary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer canary.Close()
	canaryHost := strings.TrimPrefix(canary.URL, "http://") // 127.0.0.1:port

	proxyURL := startConnectProxy(t)
	res := ProbeThrough(context.Background(), ssrfpolicy.Policy{}, proxyURL, canaryHost)
	if res.OK || res.ErrorClass != ErrClassTLSFail {
		t.Fatalf("隧道达纯 HTTP canary 应 tls_fail(证隧道通),实得 %+v", res)
	}
	if res.LatencyMS < 0 {
		t.Fatalf("延迟不应为负: %d", res.LatencyMS)
	}
}

// TLS 证书校验回归(守护"绝不能 InsecureSkipVerify"):经隧道到一个**自签证书**的 TLS canary,
// 默认配置走系统根 CA,自签 CA 不受信 → 握手必败 → tls_fail。
// 变异:把 probe_through.go 的 tls.Client 加 InsecureSkipVerify:true → 自签证书被接受 → ok=true → 本测试转红。
func TestProbeThroughVerifiesCanaryTLSCertByDefault(t *testing.T) {
	canary := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer canary.Close()
	canaryHost := strings.TrimPrefix(canary.URL, "https://") // 127.0.0.1:port

	proxyURL := startConnectProxy(t)
	res := ProbeThrough(context.Background(), ssrfpolicy.Policy{}, proxyURL, canaryHost)
	if res.OK || res.ErrorClass != ErrClassTLSFail {
		t.Fatalf("自签证书 canary 在默认校验下必 tls_fail(证未关 TLS 校验),实得 %+v", res)
	}
}

func TestProbeThroughDeadProxyIsTunnelRefused(t *testing.T) {
	// 指向一个已关闭端口的代理 → 连代理都连不上 → tunnel_refused(非 ok)。
	res := ProbeThrough(context.Background(), ssrfpolicy.Policy{}, &url.URL{Scheme: "http", Host: "127.0.0.1:1"}, "example.com:443")
	if res.OK {
		t.Fatalf("死代理不应 ok: %+v", res)
	}
	if res.ErrorClass != ErrClassTunnelRefused && res.ErrorClass != ErrClassDialTimeout {
		t.Fatalf("死代理应 tunnel_refused/dial_timeout,实得 %q", res.ErrorClass)
	}
}
