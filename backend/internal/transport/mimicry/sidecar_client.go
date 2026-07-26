package mimicry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// newEgressCorrelationID 生成一次出口拨号的关联 id；日志关联失败不影响拨号。
func newEgressCorrelationID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

var sidecarDialContext = (&net.Dialer{}).DialContext

// SidecarClient 把 Go 的 transport 路径接到本地 BoringSSL TLS sidecar。
type SidecarClient struct {
	socketPath string
	logger     *slog.Logger
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
	return NewSidecarRoundTripperForceH1(client, profileID, false)
}

// NewSidecarRoundTripperForceH1 构造可显式收窄到 HTTP/1.1 的 Rust transport。
// 默认构造器按 profile 的 ALPN 工作；本开关仅用于部署兼容和故障隔离。
func NewSidecarRoundTripperForceH1(client *SidecarClient, profileID string, forceH1 bool) http.RoundTripper {
	rt := &sidecarRoundTripper{client: client, profileID: profileID, forceH1: forceH1}
	return newSidecarTransportFromRT(rt)
}

// TargetIPResolver 在真正拨号前返回本次允许连接的目标地址。返回值会随控制帧
// 交给 Rust，Rust 只连接这些地址，同时仍使用原始域名完成 TLS SNI 和证书校验。
type TargetIPResolver func(context.Context, string) ([]netip.Addr, error)

// NewPinnedSidecarRoundTripper 构造带目标地址绑定的 Rust transport。它用于
// 管理员可配置的出站端点，防止安全检查完成后 DNS 再绑定到另一地址。
func NewPinnedSidecarRoundTripper(client *SidecarClient, profileID string, resolver TargetIPResolver) (http.RoundTripper, error) {
	if client == nil || strings.TrimSpace(profileID) == "" || resolver == nil {
		return nil, fmt.Errorf("mimicry sidecar: 目标地址绑定参数不完整")
	}
	rt := &sidecarRoundTripper{client: client, profileID: profileID, targetIPResolver: resolver}
	return newSidecarTransportFromRT(rt), nil
}

// sidecarTransport 复用 net/http 连接池，并把 profile 与账号代理绑定到 Rust 出口。
type sidecarTransport struct {
	*http.Transport
	profileID string
	boundRT   *sidecarRoundTripper
}

// SidecarProfileID 返回该 RT 绑定的 sidecar profile id,同时充当"我已走 sidecar"的导出标记。
func (s *sidecarTransport) SidecarProfileID() string {
	if s.boundRT != nil && s.boundRT.inline != nil {
		return "inline:" + s.boundRT.inline.ID
	}
	return s.profileID
}

// WithInlineTLSProfile 返回绑定动态 profile 的新 transport。原实例及连接池不被修改，
// 不同账号不会共享或串用 profile。
func (s *sidecarTransport) WithInlineTLSProfile(profile *InlineTLSProfile) (http.RoundTripper, error) {
	if s == nil || s.boundRT == nil {
		return nil, fmt.Errorf("mimicry sidecar: invalid sidecar transport")
	}
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	rt := &sidecarRoundTripper{
		client:           s.boundRT.client,
		inline:           profile.clone(),
		forceH1:          s.boundRT.forceH1,
		proxy:            s.boundRT.proxy,
		targetIPResolver: s.boundRT.targetIPResolver,
	}
	return newSidecarTransportFromRT(rt), nil
}

// newSidecarTransportFromRT 用给定的 sidecarRoundTripper(承载 profile/可选 proxy)
// 构造 sidecarTransport。WithProxy 复用它生成带代理的新实例,避免拨号参数重复。
func newSidecarTransportFromRT(rt *sidecarRoundTripper) *sidecarTransport {
	return &sidecarTransport{
		Transport: &http.Transport{
			DialTLSContext:      rt.DialTLSContext,
			ForceAttemptHTTP2:   false,
			DisableCompression:  false,
			MaxIdleConns:        256,
			MaxIdleConnsPerHost: 64,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
		profileID: rt.profileID,
		boundRT:   rt,
	}
}

// WithProxy 返回绑定同一账号代理的新 transport；代理失败不会回退直连。
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
		client:           s.boundRT.client,
		profileID:        s.profileID,
		inline:           s.boundRT.inline,
		forceH1:          s.boundRT.forceH1,
		proxy:            spec,
		targetIPResolver: s.boundRT.targetIPResolver,
	}
	return newSidecarTransportFromRT(rt), nil
}

// Inspect 通过 sidecar 自己的 ready operation 读取协议版本、能力与内置 profile。
// 它不解析 DNS、不连接任何上游，也不把任意错误误当成健康。
func (c *SidecarClient) Inspect(ctx context.Context) (*SidecarStatus, error) {
	if c == nil {
		return nil, fmt.Errorf("mimicry sidecar: nil client")
	}
	if c.socketPath == "" {
		return nil, fmt.Errorf("mimicry sidecar: empty socket path")
	}
	conn, err := sidecarDialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return nil, fmt.Errorf("%w: dial unix socket %s: %w", ErrSidecarUnavailable, c.socketPath, err)
	}
	defer conn.Close()
	stopCancel := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now())
	})
	defer stopCancel()
	if err := setDeadlineFromContext(conn, ctx); err != nil {
		return nil, err
	}
	if _, err := writeSidecarFrame(conn, sidecarControlRequest{Version: SidecarProtocolVersion, Operation: sidecarOperationReady}); err != nil {
		err = sidecarContextError(ctx, err)
		return nil, fmt.Errorf("%w: write ready frame: %w", ErrSidecarUnavailable, err)
	}
	var ack sidecarControlAck
	if err := readSidecarFrame(conn, &ack); err != nil {
		err = sidecarContextError(ctx, err)
		return nil, fmt.Errorf("%w: read ready ack: %w", ErrSidecarUnavailable, err)
	}
	if err := validateSidecarAck(ack); err != nil {
		return nil, err
	}
	return &SidecarStatus{
		Version:      ack.Version,
		Capabilities: append([]string(nil), ack.Capabilities...),
		ProfileIDs:   append([]string(nil), ack.ProfileIDs...),
	}, nil
}

func validateSidecarAck(ack sidecarControlAck) error {
	if ack.Version != SidecarProtocolVersion {
		return &SidecarError{Code: SidecarErrorProtocolUnsupported, Message: fmt.Sprintf("ACK version=%d, want=%d", ack.Version, SidecarProtocolVersion)}
	}
	if ack.OK {
		return nil
	}
	if ack.Error == nil {
		return &SidecarError{Code: SidecarErrorInternal, Message: "sidecar rejected request without error detail"}
	}
	return &SidecarError{Code: ack.Error.Code, Message: ack.Error.Message}
}

// DialTLS 拨号 Unix sidecar,发送一帧带帧长的 JSON 控制消息,等待一帧 ACK,
// 随后在 sidecar 持有的 TLS 连接之上返回一条明文流。sidecar 失败时 fail-closed;
// 本函数绝不回退到进程内 TLS 或标准库 transport。
func (c *SidecarClient) DialTLS(ctx context.Context, host string, port int, profileID string, inline *InlineTLSProfile, proxy *sidecarProxySpec) (net.Conn, error) {
	return c.dialTLS(ctx, host, port, profileID, inline, false, proxy)
}

func (c *SidecarClient) dialTLS(ctx context.Context, host string, port int, profileID string, inline *InlineTLSProfile, forceH1 bool, proxy *sidecarProxySpec) (net.Conn, error) {
	return c.dialTLSPinned(ctx, host, port, profileID, inline, forceH1, proxy, nil)
}

func (c *SidecarClient) dialTLSPinned(ctx context.Context, host string, port int, profileID string, inline *InlineTLSProfile, forceH1 bool, proxy *sidecarProxySpec, pinnedTargetIPs []string) (net.Conn, error) {
	if c == nil {
		return nil, fmt.Errorf("mimicry sidecar: nil client")
	}
	if c.socketPath == "" {
		return nil, fmt.Errorf("mimicry sidecar: empty socket path")
	}
	if host == "" || port <= 0 || port > 65535 {
		return nil, fmt.Errorf("mimicry sidecar: invalid target %q:%d", host, port)
	}
	if (profileID == "") == (inline == nil) {
		return nil, fmt.Errorf("mimicry sidecar: 必须且只能提供 profile_id 或 inline_profile")
	}
	if len(pinnedTargetIPs) > 16 {
		return nil, fmt.Errorf("mimicry sidecar: 目标地址绑定数量超过 16")
	}
	if len(pinnedTargetIPs) > 0 && proxy != nil {
		return nil, fmt.Errorf("mimicry sidecar: 目标地址绑定不能与代理隧道同时使用")
	}
	if inline != nil {
		if err := inline.Validate(); err != nil {
			return nil, err
		}
		inline = inline.clone()
	}
	correlationID := newEgressCorrelationID()
	profileLabel := profileID
	if inline != nil {
		profileLabel = "inline:" + inline.ID
	}
	obs := newSidecarDialObserver(c.logger, correlationID, host, port, profileLabel, forceH1, proxy != nil, time.Now())
	conn, err := sidecarDialContext(ctx, "unix", c.socketPath)
	if err != nil {
		obs.failed(ctx, sidecarPhaseDial, err)
		return nil, fmt.Errorf("mimicry sidecar: dial unix socket %s: %w", c.socketPath, err)
	}
	stopCancel := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now())
	})
	defer stopCancel()
	if err := setDeadlineFromContext(conn, ctx); err != nil {
		obs.failed(ctx, sidecarPhaseDial, err)
		conn.Close()
		return nil, err
	}
	req := sidecarControlRequest{
		Version:         SidecarProtocolVersion,
		Operation:       sidecarOperationConnect,
		TargetHost:      host,
		Port:            uint16(port),
		ProfileID:       profileID,
		InlineProfile:   inline,
		CorrelationID:   correlationID,
		ForceH1:         forceH1Ptr(forceH1),
		Proxy:           proxy,
		PinnedTargetIPs: append([]string(nil), pinnedTargetIPs...),
	}
	if proxy != nil {
		req.ProxyResolvedIPs = append([]string(nil), proxy.ResolvedIPs...)
	}
	frameBytes, err := writeSidecarFrame(conn, req)
	if err != nil {
		err = sidecarContextError(ctx, err)
		obs.failed(ctx, sidecarPhaseWriteControl, err)
		conn.Close()
		return nil, fmt.Errorf("mimicry sidecar: write control frame: %w", err)
	}
	var ack sidecarControlAck
	if err := readSidecarFrame(conn, &ack); err != nil {
		err = sidecarContextError(ctx, err)
		obs.failed(ctx, sidecarPhaseReadAck, err)
		conn.Close()
		return nil, fmt.Errorf("mimicry sidecar: read ack frame: %w", err)
	}
	if err := validateSidecarAck(ack); err != nil {
		conn.Close()
		obs.rejected(ctx, err)
		return nil, err
	}
	if !stopCancel() {
		if err := ctx.Err(); err != nil {
			conn.Close()
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	obs.established(ctx, frameBytes)
	return conn, nil
}

type sidecarRoundTripper struct {
	client           *SidecarClient
	profileID        string
	inline           *InlineTLSProfile
	forceH1          bool
	proxy            *sidecarProxySpec
	targetIPResolver TargetIPResolver
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
	var pinnedTargetIPs []string
	if rt.targetIPResolver != nil {
		addresses, err := rt.targetIPResolver(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("mimicry sidecar: 目标地址校验失败: %w", err)
		}
		pinnedTargetIPs, err = normalizePinnedTargetIPs(addresses)
		if err != nil {
			return nil, err
		}
	}
	proxy := rt.proxy
	if proxy != nil {
		proxy = proxy.clone()
		proxyURL := &url.URL{
			Scheme: proxy.Scheme,
			Host:   net.JoinHostPort(proxy.Host, strconv.Itoa(int(proxy.Port))),
		}
		addresses, err := provider.ResolveProxyEndpointIPs(ctx, proxyURL)
		if err != nil {
			return nil, fmt.Errorf("mimicry sidecar: 代理地址校验失败: %w", err)
		}
		proxy.ResolvedIPs, err = normalizePinnedTargetIPs(addresses)
		if err != nil {
			return nil, fmt.Errorf("mimicry sidecar: 代理地址绑定失败: %w", err)
		}
	}
	return rt.client.dialTLSPinned(ctx, host, port, rt.profileID, rt.inline, rt.forceH1, proxy, pinnedTargetIPs)
}

func normalizePinnedTargetIPs(addresses []netip.Addr) ([]string, error) {
	if len(addresses) == 0 || len(addresses) > 16 {
		return nil, fmt.Errorf("mimicry sidecar: 目标地址绑定数量必须为 1..16")
	}
	out := make([]string, 0, len(addresses))
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if !address.IsValid() {
			return nil, fmt.Errorf("mimicry sidecar: 目标地址绑定包含无效地址")
		}
		if _, exists := seen[address]; exists {
			continue
		}
		seen[address] = struct{}{}
		out = append(out, address.String())
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("mimicry sidecar: 目标地址绑定为空")
	}
	return out, nil
}

func forceH1Ptr(forceH1 bool) *bool {
	if !forceH1 {
		return nil
	}
	return &forceH1
}

// sidecarProxySpec 是下发给 Rust sidecar 的结构化代理载荷,字段与 Rust proto::ProxySpec 对齐。
// 结构化下发(而非原始 URL):password 等敏感段不混进一个可被整体打印的字符串。Username/Password
// 仅在带认证时出现(omitempty),无认证时不写键。
type sidecarProxySpec struct {
	Scheme      string   `json:"scheme"`
	Host        string   `json:"host"`
	Port        uint16   `json:"port"`
	Username    string   `json:"username,omitempty"`
	Password    string   `json:"password,omitempty"`
	ResolvedIPs []string `json:"-"`
}

func (s *sidecarProxySpec) clone() *sidecarProxySpec {
	if s == nil {
		return nil
	}
	clone := *s
	clone.ResolvedIPs = append([]string(nil), s.ResolvedIPs...)
	return &clone
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

func sidecarContextError(ctx context.Context, err error) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	return err
}
