package mimicry

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// newEgressCorrelationID 生成一次出口拨号的关联 id(8 字节 hex)。best-effort:crypto/rand
// 极罕见失败时返回空串,绝不因生成日志关联 id 而让拨号失败(可观测不得反噬可用性)。
func newEgressCorrelationID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

const (
	SidecarProfileAnthropicCLIMimicryV1 = "anthropic-cli-mimicry-v1"
	sidecarMaxFrameLen                  = 1024 * 1024
)

var sidecarDialContext = (&net.Dialer{}).DialContext

// SidecarClient 把 Go 的 transport 路径接到本地 BoringSSL TLS sidecar。
type SidecarClient struct {
	socketPath string
	// logger 为 go↔rust 出口边界的分层结构化日志目标;nil 时兜底 slog.Default()
	// (片 D 门面)。测试经 WithLogger 注入收集型 handler 断言边界事件。
	logger *slog.Logger
}

func NewSidecarClient(socketPath string) *SidecarClient {
	return &SidecarClient{socketPath: socketPath}
}

// WithLogger 注入出口边界日志的 logger,返回自身便于链式;nil 时用 slog.Default()。
func (c *SidecarClient) WithLogger(logger *slog.Logger) *SidecarClient {
	if c != nil {
		c.logger = logger
	}
	return c
}

func NewSidecarRoundTripper(client *SidecarClient, profileID string) http.RoundTripper {
	return NewSidecarRoundTripperForceH1(client, profileID, forceH1Enabled())
}

// NewSidecarRoundTripperForceH1 在 NewSidecarRoundTripper 基础上显式指定 forceH1。
// forceH1=true 时,每次拨号的 control request 携带 force_h1,令 Rust sidecar 握手只广告
// ALPN=http/1.1,从根消除 h2 升级(与 Go uTLS 路 utls_dialer.go 的 ForceH1 姿态一致)。
// sidecarTransport 包装走 Rust tls-sidecar 的 *http.Transport,并暴露 SidecarProfileID()
// 标记。转发层(gateway.UpstreamDispatcher.applyTLSProfile)据此识别"该 RT 已走 sidecar、
// 自带内置真指纹",短路 per-account DB uTLS profile 的整体替换,避免绑定 DB profile 的账号
// 让 sidecar 永远轮不到用。内嵌 *http.Transport:RoundTrip 与连接池/DialTLSContext 行为
// 全部继承(对 net/http 完全等价)。
//
// 代理穿透(②-3):sidecarTransport 实现 WithProxy 成为 provider.proxyAwareRoundTripper,
// provider.WrapTransportWithProxy 命中该接口分支后,返回一个带 proxyURL 的新 sidecarTransport。
// 拨号时把已校验的 proxyURL 转成 sidecarProxySpec 填进 control request,令 Rust 先经代理建隧道
// 再在隧道之上握手——出口 IP 走代理,JA3/JA4 仍是伪装指纹。绑账号级代理的账号因此也能用 sidecar
// (此前会落到 WrapTransportWithProxy 的 fail-loud 分支,根本不可用)。
type sidecarTransport struct {
	*http.Transport
	profileID string
	// boundRT 是驱动本 transport 拨号的 sidecarRoundTripper(承载 client/forceH1/可选 proxy)。
	// WithProxy 据此派生带代理的新实例,无需从闭包里反解配置。
	boundRT *sidecarRoundTripper
}

// SidecarProfileID 返回该 RT 绑定的 sidecar profile id,同时充当"我已走 sidecar"的导出标记。
func (s *sidecarTransport) SidecarProfileID() string { return s.profileID }

func NewSidecarRoundTripperForceH1(client *SidecarClient, profileID string, forceH1 bool) http.RoundTripper {
	rt := &sidecarRoundTripper{client: client, profileID: profileID, forceH1: forceH1}
	return newSidecarTransportFromRT(rt)
}

// newSidecarTransportFromRT 用给定的 sidecarRoundTripper(承载 profileID/forceH1/可选 proxy)
// 构造 sidecarTransport。WithProxy 复用它生成带代理的新实例,避免拨号参数重复。
func newSidecarTransportFromRT(rt *sidecarRoundTripper) *sidecarTransport {
	return &sidecarTransport{
		Transport: &http.Transport{
			DialTLSContext:      rt.DialTLSContext,
			ForceAttemptHTTP2:   false,
			DisableCompression:  false,
			MaxIdleConns:        256,
			MaxIdleConnsPerHost: 64, // DM-17:默认 2 在网关负载下复用近失效
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
		profileID: rt.profileID,
		boundRT:   rt,
	}
}

// WithProxy 让 sidecarTransport 满足 provider.proxyAwareRoundTripper:把【已经过 proxyadmin
// SSRF 校验的】proxyURL 转成 sidecarProxySpec,返回一个绑定该代理的新 sidecarTransport(不改
// 原实例)。每次拨号都会把该 spec 填进 control request 下发给 Rust sidecar。
//
// scheme 校验:仅放行 http/https/socks5(socks5h 归一为 socks5)。不支持的 scheme **fail-loud
// 返回 err**——经 provider.WrapTransportWithProxy 的 buildErr 路径仍 fail-closed(RoundTrip 返回
// 该 err),绝不静默回退直连,杜绝真实出口 IP 泄露。SSRF 校验留在 Go proxyadmin 写时层,这里
// 只透传已校验的 proxyURL。
func (s *sidecarTransport) WithProxy(proxyURL *url.URL) (http.RoundTripper, error) {
	if proxyURL == nil {
		// 无代理 = 不下发 proxy 字段,沿用当前 RT(零开销直连)。
		return s, nil
	}
	spec, err := proxySpecFromURL(proxyURL)
	if err != nil {
		return nil, err
	}
	rt := &sidecarRoundTripper{
		client:    s.boundRT.client,
		profileID: s.profileID,
		forceH1:   s.boundRT.forceH1,
		proxy:     spec,
	}
	return newSidecarTransportFromRT(rt), nil
}

// DialTLS 拨号 Unix sidecar,发送一帧带帧长的 JSON 控制消息,等待一帧 ACK,
// 随后在 sidecar 持有的 TLS 连接之上返回一条明文流。sidecar 失败时 fail-closed;
// 本函数绝不回退到 uTLS 或标准库 transport。
func (c *SidecarClient) DialTLS(ctx context.Context, host string, port int, profileID string, forceH1 bool, proxy *sidecarProxySpec) (net.Conn, error) {
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
	correlationID := newEgressCorrelationID()
	obs := newSidecarDialObserver(c.logger, correlationID, host, port, profileID, forceH1, proxy != nil, time.Now())
	conn, err := sidecarDialContext(ctx, "unix", c.socketPath)
	if err != nil {
		obs.failed(ctx, sidecarPhaseDial, err)
		return nil, fmt.Errorf("mimicry sidecar: dial unix socket %s: %w", c.socketPath, err)
	}
	if err := setDeadlineFromContext(conn, ctx); err != nil {
		obs.failed(ctx, sidecarPhaseDial, err)
		conn.Close()
		return nil, err
	}
	req := sidecarControlRequest{
		TargetHost:    host,
		Port:          uint16(port),
		ProfileID:     profileID,
		CorrelationID: correlationID,
		ForceH1:       forceH1Ptr(forceH1),
		Proxy:         proxy,
	}
	frameBytes, err := writeSidecarFrame(conn, req)
	if err != nil {
		obs.failed(ctx, sidecarPhaseWriteControl, err)
		conn.Close()
		return nil, fmt.Errorf("mimicry sidecar: write control frame: %w", err)
	}
	var ack sidecarControlAck
	if err := readSidecarFrame(conn, &ack); err != nil {
		obs.failed(ctx, sidecarPhaseReadAck, err)
		conn.Close()
		return nil, fmt.Errorf("mimicry sidecar: read ack frame: %w", err)
	}
	if !ack.OK {
		conn.Close()
		if ack.Error == "" {
			ack.Error = "sidecar rejected request"
		}
		obs.rejected(ctx, ack.Error)
		return nil, fmt.Errorf("mimicry sidecar: %s", ack.Error)
	}
	_ = conn.SetDeadline(time.Time{})
	obs.established(ctx, frameBytes)
	return conn, nil
}

type sidecarRoundTripper struct {
	client    *SidecarClient
	profileID string
	// forceH1 为 true 时,每次 DialTLS 都让 sidecar 握手只广告 ALPN=http/1.1。
	forceH1 bool
	// proxy 非 nil 时,每次 DialTLS 都把该代理 spec 填进 control request,令 Rust 先经代理建隧道
	// 再握手(②-3 代理穿透)。nil = 直连目标(今日行为)。
	proxy *sidecarProxySpec
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
	return rt.client.DialTLS(ctx, host, port, rt.profileID, rt.forceH1, rt.proxy)
}

type sidecarControlRequest struct {
	TargetHost string `json:"target_host"`
	Port       uint16 `json:"port"`
	ProfileID  string `json:"profile_id"`
	// CorrelationID 随控制帧过河给 Rust sidecar,令 go↔rust 两侧日志用同一 id 关联(跨边界
	// 追一次出口握手)。omitempty:空时不写键,老 Rust sidecar(无此字段且不 deny_unknown_fields)
	// 直接忽略,向后兼容。Rust 侧 proto::ControlRequest 加同名 serde(default) 字段后即可 tracing 记录。
	CorrelationID string `json:"correlation_id,omitempty"`
	// ForceH1 仅在非 nil 时随 control frame 下发(omitempty + 指针);nil(默认,旧线缆)时
	// JSON 不含 force_h1 键,与历史 Rust sidecar 完全兼容。Rust 侧 serde(default) 把缺省解为 None。
	ForceH1 *bool `json:"force_h1,omitempty"`
	// Proxy 仅在非 nil 时随 control frame 下发(omitempty + 指针);nil(默认,无账号级代理=直连)
	// 时 JSON 不含 proxy 键,与历史 Rust sidecar 完全兼容。Rust 侧 serde(default) 把缺省解为 None。
	Proxy *sidecarProxySpec `json:"proxy,omitempty"`
}

// sidecarProxySpec 是下发给 Rust sidecar 的结构化代理载荷,字段与 Rust proto::ProxySpec 对齐。
// 结构化下发(而非原始 URL):password 等敏感段不混进一个可被整体打印的字符串。Username/Password
// 仅在带认证时出现(omitempty),无认证时不写键。
type sidecarProxySpec struct {
	Scheme   string `json:"scheme"`
	Host     string `json:"host"`
	Port     uint16 `json:"port"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// proxySpecFromURL 把【已经过 proxyadmin SSRF 校验的】proxyURL 拆成 sidecarProxySpec。
// scheme 仅放行 http/https/socks5(socks5h 归一为 socks5);其余 scheme **fail-loud 返回 err**,
// 经 provider.WrapTransportWithProxy 仍 fail-closed,绝不静默直连。端口按 scheme 补默认值。
func proxySpecFromURL(proxyURL *url.URL) (*sidecarProxySpec, error) {
	if proxyURL == nil {
		return nil, fmt.Errorf("mimicry sidecar: nil proxy url")
	}
	scheme := strings.ToLower(proxyURL.Scheme)
	switch scheme {
	case "http", "https":
		// 保留原 scheme,Rust 侧据此决定是否对代理本身做 TLS。
	case "socks5", "socks5h":
		// socks5h 与 socks5 在我们的用法下等价(domain atyp 让代理端解析目标),归一化。
		scheme = "socks5"
	default:
		return nil, fmt.Errorf("mimicry sidecar: 暂不支持的代理 scheme %q(仅支持 http/https/socks5)", proxyURL.Scheme)
	}

	host := proxyURL.Hostname()
	if host == "" {
		return nil, fmt.Errorf("mimicry sidecar: 代理 URL 缺少 host")
	}
	port, err := proxyPortForScheme(proxyURL, scheme)
	if err != nil {
		return nil, err
	}

	spec := &sidecarProxySpec{Scheme: scheme, Host: host, Port: port}
	if u := proxyURL.User; u != nil {
		spec.Username = u.Username()
		if pw, ok := u.Password(); ok {
			spec.Password = pw
		}
	}
	return spec, nil
}

// proxyPortForScheme 取代理端口:URL 显式给端口则用之,否则按 scheme 补默认(https=443、
// http=80、socks5=1080)。端口非法 fail-loud。
func proxyPortForScheme(proxyURL *url.URL, scheme string) (uint16, error) {
	if p := proxyURL.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 || n > 65535 {
			return 0, fmt.Errorf("mimicry sidecar: 代理端口非法 %q", p)
		}
		return uint16(n), nil
	}
	switch scheme {
	case "https":
		return 443, nil
	case "socks5":
		return 1080, nil
	default:
		return 80, nil
	}
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

// writeSidecarFrame 发一帧长度前缀 + JSON body,返回 body 字节数(供出口帧传输层
// 观测)。写失败返回 (0, err)。
func writeSidecarFrame(conn net.Conn, value any) (int, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	if len(body) > sidecarMaxFrameLen {
		return 0, fmt.Errorf("frame length %d exceeds max %d", len(body), sidecarMaxFrameLen)
	}
	var prefix [4]byte
	binary.LittleEndian.PutUint32(prefix[:], uint32(len(body)))
	if _, err := conn.Write(prefix[:]); err != nil {
		return 0, err
	}
	if _, err := conn.Write(body); err != nil {
		return 0, err
	}
	return len(body), nil
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
