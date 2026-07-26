package mimicry

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// proxyAwareShape 镜像 provider 包里【未导出的】proxyAwareRoundTripper 接口形状
// (WithProxy(*url.URL) (http.RoundTripper, error))。provider.WrapTransportWithProxy 用
// 该接口分支识别"能自行在握手之下注入代理"的 RT。这里本地复刻接口仅为断言 sidecarTransport
// 满足该形状——若 WithProxy 签名漂移,接口断言会编译失败/运行失败,守护跨包接线不被悄悄改坏。
type proxyAwareShape interface {
	WithProxy(*url.URL) (http.RoundTripper, error)
}

// 抓的缺陷:sidecarTransport 必须实现 proxyAwareRoundTripper 形状,否则
// provider.WrapTransportWithProxy 不会命中代理穿透分支,绑代理的 sidecar 账号会落回 fail-loud
// 分支永远不可用。变异证:把 WithProxy 方法删掉/改签名,本断言编译或运行失败转红。
func TestSidecarTransportSatisfiesProxyAwareShape(t *testing.T) {
	client := NewSidecarClient("/tmp/tls-sidecar.sock")
	rt := NewSidecarRoundTripper(client, SidecarProfileAnthropicCLIMimicryV1)

	if _, ok := rt.(proxyAwareShape); !ok {
		t.Fatalf("sidecarTransport 必须实现 proxyAwareRoundTripper 形状(WithProxy),实际 %T", rt)
	}
}

// 抓的缺陷:WithProxy 把已校验的 proxyURL 正确转成 control request 的 proxy 字段——scheme/host/
// port/username/password 必须如实下发,否则 Rust 端建隧道时拿不到正确目标/凭据,代理穿透失效。
// 变异证:把 WithProxy 实现成"忽略 proxy 静默返回自己"(return s,nil),control frame 里 proxy
// 会缺失,本测试断言 req.Proxy==nil 而转红——证明它没真正把代理穿进帧。
func TestSidecarTransportWithProxyFillsControlRequest(t *testing.T) {
	req := dialWithProxyAndCaptureRequest(t, "http://alice:s3cr3t@proxy.example.com:3128")

	if req.Proxy == nil {
		t.Fatal("WithProxy 后 control request 必须携带 proxy 字段(不得静默丢弃,否则真实出口 IP 泄露)")
	}
	if req.Proxy.Scheme != "http" {
		t.Errorf("proxy.scheme = %q, want http", req.Proxy.Scheme)
	}
	if req.Proxy.Host != "proxy.example.com" {
		t.Errorf("proxy.host = %q, want proxy.example.com", req.Proxy.Host)
	}
	if req.Proxy.Port != 3128 {
		t.Errorf("proxy.port = %d, want 3128", req.Proxy.Port)
	}
	if req.Proxy.Username != "alice" {
		t.Errorf("proxy.username = %q, want alice", req.Proxy.Username)
	}
	if req.Proxy.Password != "s3cr3t" {
		t.Errorf("proxy.password = %q, want s3cr3t", req.Proxy.Password)
	}
	if len(req.ProxyResolvedIPs) != 1 || req.ProxyResolvedIPs[0] != "1.1.1.1" {
		t.Errorf("代理解析结果必须绑定进独立字段,got %v", req.ProxyResolvedIPs)
	}
}

// 抓的缺陷:socks5h 必须归一为 socks5(Rust 侧用 domain atyp 让代理端解析,二者等价),
// 且无认证时 username/password 键被省略(omitempty)。变异证:去掉 socks5h 归一分支,
// scheme 会是 "socks5h" 而转红;去掉 omitempty,无认证 JSON 会含 username/password 键。
func TestSidecarTransportWithProxySocks5hNormalizedAndCredentialsOmitted(t *testing.T) {
	req, rawJSON := dialWithProxyAndCaptureRequestJSON(t, "socks5h://10.0.0.9:1080")

	if req.Proxy == nil {
		t.Fatal("socks5h 代理必须下发 proxy 字段")
	}
	if req.Proxy.Scheme != "socks5" {
		t.Errorf("socks5h 必须归一为 socks5,got %q", req.Proxy.Scheme)
	}
	if req.Proxy.Port != 1080 {
		t.Errorf("socks5 默认端口应为 1080,got %d", req.Proxy.Port)
	}
	if strings.Contains(rawJSON, "username") || strings.Contains(rawJSON, "password") {
		t.Errorf("无认证代理的 JSON 不得含 username/password 键(omitempty),got %s", rawJSON)
	}
}

// 抓的缺陷:无代理(直连)时控制帧不得含 proxy 键(omitempty),保持发往老 sidecar 的旧线缆字节。
// 变异证:去掉 sidecarControlRequest.Proxy 的 omitempty,nil 会输出 "proxy":null,断言转红。
func TestSidecarControlRequestOmitsProxyWhenNil(t *testing.T) {
	req := sidecarControlRequest{
		TargetHost: "api.anthropic.com",
		Port:       443,
		ProfileID:  SidecarProfileAnthropicCLIMimicryV1,
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "proxy") {
		t.Fatalf("proxy=nil 时 JSON 不得含 proxy 键(omitempty),got %s", raw)
	}
}

// 抓的缺陷:不支持的代理 scheme 必须 fail-loud 返回 err(经 provider 路径仍 fail-closed),
// 绝不静默直连。变异证:把 proxySpecFromURL 的 default 分支改成"放行/默认 http",
// WithProxy 会返回 nil err 而转红——证明非法 scheme 没有被悄悄吞掉。
func TestSidecarTransportWithProxyUnsupportedSchemeFailsLoud(t *testing.T) {
	client := NewSidecarClient("/tmp/tls-sidecar.sock")
	rt := NewSidecarRoundTripper(client, SidecarProfileAnthropicCLIMimicryV1).(proxyAwareShape)

	proxyURL, err := url.Parse("ftp://proxy.example.com:21")
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := rt.WithProxy(proxyURL)
	if err == nil {
		t.Fatalf("不支持的 scheme 必须 fail-loud 返回 err,却返回 %T", wrapped)
	}
	if !strings.Contains(err.Error(), "scheme") {
		t.Errorf("错误应点名不支持的 scheme,got %v", err)
	}
}

// 抓的缺陷:WithProxy(nil) 必须返回原 RT 不变(零开销直连),不得 panic 或派生空代理帧。
func TestSidecarTransportWithProxyNilReturnsSelf(t *testing.T) {
	client := NewSidecarClient("/tmp/tls-sidecar.sock")
	rt := NewSidecarRoundTripper(client, SidecarProfileAnthropicCLIMimicryV1)
	pa := rt.(proxyAwareShape)

	got, err := pa.WithProxy(nil)
	if err != nil {
		t.Fatalf("WithProxy(nil) 不应报错,got %v", err)
	}
	if got != rt {
		t.Errorf("WithProxy(nil) 应返回原 RT 不变,got %T", got)
	}
}

// dialWithProxyAndCaptureRequest 起一个假 sidecar 服务端,经 WithProxy(proxyRaw) 派生的 RT
// 拨号一次,捕获并返回服务端收到的 control request(供断言 proxy 字段)。
func dialWithProxyAndCaptureRequest(t *testing.T, proxyRaw string) sidecarControlRequest {
	t.Helper()
	req, _ := dialWithProxyAndCaptureRequestJSON(t, proxyRaw)
	return req
}

// dialWithProxyAndCaptureRequestJSON 同上,但同时返回 control request 的原始 JSON 字节,
// 供 omitempty 这类需要看线缆字节的断言。
func dialWithProxyAndCaptureRequestJSON(t *testing.T, proxyRaw string) (sidecarControlRequest, string) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()

	oldDial := sidecarDialContext
	sidecarDialContext = func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	}
	defer func() { sidecarDialContext = oldDial }()

	type captured struct {
		req sidecarControlRequest
		raw string
		err error
	}
	resultCh := make(chan captured, 1)
	go func() {
		conn := serverConn
		var prefix [4]byte
		if _, err := io.ReadFull(conn, prefix[:]); err != nil {
			resultCh <- captured{err: err}
			return
		}
		n := binary.LittleEndian.Uint32(prefix[:])
		body := make([]byte, n)
		if _, err := io.ReadFull(conn, body); err != nil {
			resultCh <- captured{err: err}
			return
		}
		var req sidecarControlRequest
		err := json.Unmarshal(body, &req)
		resultCh <- captured{req: req, raw: string(body), err: err}
		// 回 ACK 让客户端 DialTLS 顺利返回。
		writeSidecarTestFrame(t, conn, []byte(`{"version":4,"ok":true}`))
	}()

	proxyURL, err := url.Parse(proxyRaw)
	if err != nil {
		t.Fatalf("parse proxy url %q: %v", proxyRaw, err)
	}
	if address, parseErr := netip.ParseAddr(proxyURL.Hostname()); parseErr == nil && address.IsPrivate() {
		allowPrivateProxy(t, proxyURL.Hostname())
	} else if parseErr != nil {
		restore := provider.SwapProxyEndpointLookupForTesting(
			func(context.Context, string, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
			},
		)
		t.Cleanup(restore)
	}
	client := NewSidecarClient("/tmp/tls-sidecar.sock")
	base := NewSidecarRoundTripper(client, SidecarProfileAnthropicCLIMimicryV1).(proxyAwareShape)
	wrapped, err := base.WithProxy(proxyURL)
	if err != nil {
		t.Fatalf("WithProxy(%q): %v", proxyRaw, err)
	}
	st, ok := wrapped.(*sidecarTransport)
	if !ok {
		t.Fatalf("WithProxy 应返回 *sidecarTransport,got %T", wrapped)
	}

	// 直接驱动派生 RT 的 DialTLSContext,触发一次带代理的 control frame 下发。
	conn, dialErr := st.boundRT.DialTLSContext(context.Background(), "tcp", "api.anthropic.com:443")
	if conn != nil {
		conn.Close()
	}
	if dialErr != nil {
		_ = clientConn.Close()
	}
	res := <-resultCh
	if res.err != nil {
		t.Fatalf("捕获 control request 失败: %v(dialErr=%v)", res.err, dialErr)
	}
	if dialErr != nil {
		t.Fatalf("DialTLSContext 失败: %v", dialErr)
	}
	return res.req, res.raw
}
