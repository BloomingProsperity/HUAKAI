package mimicry

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/respdecompress"
	utls "github.com/refraction-networking/utls"
)

// UtlsDialer 是 net.Dialer 风格的 TLS 拨号包装器。
type UtlsDialer struct {
	Template         *ClientHelloTemplate
	NetDialer        net.Dialer
	ProxyDialer      ProxyDialerFunc
	TLSConfig        *utls.Config
	HandshakeTimeout time.Duration
	// ForceH1 为 true 时,握手在线缆上只广告 ALPN=http/1.1,不广告 h2。
	// 默认只锁单一真实客户端档案的协议栈，避免混用不自洽的握手与 HTTP 层。
	// 这同时是协议正确性约束:本路径返回的是 *utls.UConn(非标准库 *tls.Conn),
	// Go 的内置 HTTP/2 升级路径在结构上接不住它,若仍广告 h2 且服务端选 h2,
	// 会出现"服务端按 h2、客户端发 h1 帧"的握手后错乱。收窄 ALPN 从根上消除。
	ForceH1 bool
}

// NewUtlsDialer 返回使用指定 ClientHello 模板的拨号器。
// ForceH1 默认由运维 env HUAKAI_TRANSPORT_FORCE_H1 决定(默认开)。
func NewUtlsDialer(template *ClientHelloTemplate) *UtlsDialer {
	return &UtlsDialer{
		Template: template,
		NetDialer: net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		},
		HandshakeTimeout: 10 * time.Second,
		ForceH1:          forceH1Enabled(),
	}
}

// forceH1Enabled 读运维开关 HUAKAI_TRANSPORT_FORCE_H1。空或非 "false" 一律视为
// 开启(默认强制 H1),仅当显式设为 "false" 时关闭。与 factory.go 直接读
// HUAKAI_TRANSPORT_PHASE_A_FALLBACK 的 transport 层就地读 env 惯例保持一致,
// 避免反向 import config 包。关闭仅适用于 BoringSSL sidecar 等能自洽出 h2 的
// 出口;Go-native uTLS 路径应保持默认开,否则进入 h2-广告但 h1-收发 的危险中间态。
func forceH1Enabled() bool {
	return os.Getenv("HUAKAI_TRANSPORT_FORCE_H1") != "false"
}

// NewRoundTripper 构造使用 uTLS DialTLSContext 的 http.RoundTripper。
func NewRoundTripper(template *ClientHelloTemplate) http.RoundTripper {
	dialer := NewUtlsDialer(template)
	return &roundTripper{
		// Phase A 保持直连。Go 的 HTTPS proxy 路径不会调用
		// DialTLSContext，若暴露 *http.Transport 会把 uTLS 旁路掉。
		inner: &http.Transport{
			DialContext:    dialer.NetDialer.DialContext,
			DialTLSContext: dialer.DialTLS,
			// 出口走 uTLS 自定义握手,返回 *utls.UConn 非标准库 *tls.Conn,
			// Go 内置 h2 升级路径接不住它,故此布尔对实际协议无支配力;
			// 显式置 false 消除"意图含糊",与 sidecar_client.go 的姿态对齐。
			// 真正决定 H1/H2 的是握手 ALPN(见 UtlsDialer.ForceH1)。
			ForceAttemptHTTP2:     false,
			MaxIdleConns:          256,
			MaxIdleConnsPerHost:   64, // DM-17
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
		template: template,
	}
}

type roundTripper struct {
	inner    *http.Transport
	template *ClientHelloTemplate
}

func (rt *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// 反封禁 AE 解压链:在线缆上呈现浏览器风格的 Accept-Encoding(gzip, deflate,
	// br, zstd)。Clone 避免改动调用方请求(RoundTripper 契约)。一旦我们显式设置
	// Accept-Encoding,Go transport 便不再透明解码,故响应解码由我们负责。
	r2 := req.Clone(req.Context())
	r2.Header.Set("Accept-Encoding", respdecompress.BrowserAcceptEncoding)
	resp, err := rt.inner.RoundTrip(r2)
	if err != nil {
		return resp, err
	}
	return decodeMimicryResponse(resp), nil
}

// decodeMimicryResponse 按 Content-Encoding 解码响应体(gzip/deflate/br/zstd)。
// 不支持的编码或解码器构造失败时原样返回(fail-safe:绝不弄坏响应)。解码成功后
// 去掉 Content-Encoding/Content-Length 头并标记 Uncompressed。
func decodeMimicryResponse(resp *http.Response) *http.Response {
	if resp == nil || resp.Body == nil {
		return resp
	}
	enc := resp.Header.Get("Content-Encoding")
	if !respdecompress.Supported(enc) {
		return resp
	}
	decoded, derr := respdecompress.Wrap(resp.Body, enc)
	if derr != nil {
		return resp
	}
	resp.Body = decoded
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Content-Length")
	resp.ContentLength = -1
	resp.Uncompressed = true
	return resp
}

// DialTLS 建立 TCP 连接后用 uTLS + 自定义 ClientHello 完成握手。
func (d *UtlsDialer) DialTLS(ctx context.Context, network, addr string) (net.Conn, error) {
	if d == nil {
		return nil, fmt.Errorf("mimicry: nil utls dialer")
	}
	if d.Template == nil {
		return nil, fmt.Errorf("mimicry: nil clienthello template")
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("mimicry: split dial address %q: %w", addr, err)
	}
	raw, err := d.dialRaw(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	cfg := d.tlsConfig(host, d.ForceH1)
	preset := ""
	if d.Template != nil {
		preset = d.Template.Preset
	}
	var conn *utls.UConn
	if id, ok := clientHelloIDForPreset(preset); ok {
		// UTLS-05: uTLS 内置浏览器 ClientHello (真实当前 Chrome/...)。
		// 内置 hello 的 ALPN 烘焙在 parrot 里(如 Chrome 广告 h2),force-h1 在此
		// 路径只能靠上面 tlsConfig 设的 cfg.NextProtos 兜底,无法改写 parrot 扩展。
		conn = utls.UClient(raw, cfg, id)
	} else {
		spec, serr := d.Template.utlsSpec(host)
		if serr != nil {
			raw.Close()
			return nil, serr
		}
		// 自定义模板路径:force-h1 时把 spec 里的 ALPN 扩展收窄成只剩 http/1.1。
		// ApplyPreset 会用 spec 的 ALPN 同时改写线缆广告与 uc.config.NextProtos,
		// 故在此收窄即可让 ClientHello 只广告 http/1.1、且 checkALPN 只接受 http/1.1。
		// 不改 d.Template(保持真抓包保真值,捕获回归测试不破)。
		if d.ForceH1 {
			narrowSpecALPNToHTTP1(spec)
		}
		conn = utls.UClient(raw, cfg, utls.HelloCustom)
		if perr := conn.ApplyPreset(spec); perr != nil {
			raw.Close()
			return nil, perr
		}
	}
	handshakeCtx := ctx
	var cancel context.CancelFunc
	if d.HandshakeTimeout > 0 {
		handshakeCtx, cancel = context.WithTimeout(ctx, d.HandshakeTimeout)
		defer cancel()
	}
	if err := conn.HandshakeContext(handshakeCtx); err != nil {
		raw.Close()
		return nil, err
	}
	return conn, nil
}

func (d *UtlsDialer) tlsConfig(serverName string, forceH1 bool) *utls.Config {
	var cfg *utls.Config
	if d.TLSConfig != nil {
		cfg = d.TLSConfig.Clone()
	} else {
		cfg = &utls.Config{}
	}
	if cfg.ServerName == "" {
		cfg.ServerName = serverName
	}
	if forceH1 {
		// 强制 H1:Go 侧 ALPN 意图只剩 http/1.1。内置 parrot 路径不走 ApplyPreset
		// 的 ALPN 改写,此处是该路径唯一能收窄 ALPN 的兜底点;自定义模板路径则由
		// narrowSpecALPNToHTTP1 在 spec 上权威收窄(会覆盖这里)。
		cfg.NextProtos = []string{"http/1.1"}
	} else if len(cfg.NextProtos) == 0 && len(d.Template.ALPNProtocols) > 0 {
		cfg.NextProtos = append([]string(nil), d.Template.ALPNProtocols...)
	}
	return cfg
}

// narrowSpecALPNToHTTP1 把 uTLS spec 里的 ALPN 扩展原地收窄成只剩 http/1.1。
// 只动 spec(拨号期临时构造物),不动模板结构体,从而真抓包保真度与捕获回归
// 测试不受影响。spec 内无 ALPN 扩展则不操作(不凭空注入)。
func narrowSpecALPNToHTTP1(spec *utls.ClientHelloSpec) {
	if spec == nil {
		return
	}
	for _, ext := range spec.Extensions {
		if alpn, ok := ext.(*utls.ALPNExtension); ok {
			alpn.AlpnProtocols = []string{"http/1.1"}
		}
	}
}

// UTLSSpec 按模板构造 uTLS 规格；serverName 用占位主机保证 SNI 扩展可见。
func (t *ClientHelloTemplate) UTLSSpec() (*utls.ClientHelloSpec, error) {
	return t.utlsSpec("api.anthropic.com")
}

func (t *ClientHelloTemplate) utlsSpec(serverName string) (*utls.ClientHelloSpec, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	exts := t.utlsExtensions(serverName)
	return &utls.ClientHelloSpec{
		CipherSuites:       append([]uint16(nil), t.CipherSuites...),
		CompressionMethods: []uint8{0},
		Extensions:         exts,
		TLSVersMin:         minUint16(t.SupportedVersions),
		TLSVersMax:         maxUint16(t.SupportedVersions),
	}, nil
}

func (t *ClientHelloTemplate) utlsExtensions(serverName string) []utls.TLSExtension {
	out := make([]utls.TLSExtension, 0, len(t.Extensions))
	for _, id := range t.Extensions {
		switch id {
		case 0:
			out = append(out, &utls.SNIExtension{ServerName: serverName})
		case 5:
			out = append(out, &utls.StatusRequestExtension{})
		case 10:
			out = append(out, &utls.SupportedCurvesExtension{Curves: curves(t.EllipticCurves)})
		case 11:
			out = append(out, &utls.SupportedPointsExtension{SupportedPoints: ecPoints(t.ECPointFormats)})
		case 13:
			out = append(out, &utls.SignatureAlgorithmsExtension{SupportedSignatureAlgorithms: sigs(t.SignatureAlgorithms)})
		case 16:
			out = append(out, &utls.ALPNExtension{AlpnProtocols: alpns(t.ALPNProtocols)})
		case 18:
			out = append(out, &utls.SCTExtension{})
		case 21:
			out = append(out, &utls.UtlsPaddingExtension{PaddingLen: paddingLen(t.PaddingLen), WillPad: true})
		case 23:
			out = append(out, &utls.ExtendedMasterSecretExtension{})
		case 35:
			out = append(out, &utls.SessionTicketExtension{})
		case 43:
			out = append(out, &utls.SupportedVersionsExtension{Versions: append([]uint16(nil), t.SupportedVersions...)})
		case 45:
			out = append(out, &utls.PSKKeyExchangeModesExtension{Modes: pskModes(t.PSKModes)})
		case 51:
			out = append(out, &utls.KeyShareExtension{KeyShares: keyShares(t.KeyShareGroups, t.EllipticCurves)})
		case 65037:
			out = append(out, &utls.GREASEEncryptedClientHelloExtension{CandidatePayloadLens: []uint16{128}})
		case 65281:
			out = append(out, &utls.RenegotiationInfoExtension{Renegotiation: utls.RenegotiateOnceAsClient})
		default:
			// Phase A 模板只记录未知扩展编号；payload 在净化模板中不可用。
			out = append(out, &utls.GenericExtension{Id: id})
		}
	}
	return out
}

func curves(in []uint16) []utls.CurveID {
	out := make([]utls.CurveID, len(in))
	for i, v := range in {
		out[i] = utls.CurveID(v)
	}
	return out
}

func sigs(in []uint16) []utls.SignatureScheme {
	out := make([]utls.SignatureScheme, len(in))
	for i, v := range in {
		out[i] = utls.SignatureScheme(v)
	}
	return out
}

func keyShares(groups, fallback []uint16) []utls.KeyShare {
	if len(groups) == 0 && len(fallback) > 0 {
		groups = fallback[:1]
	}
	out := make([]utls.KeyShare, len(groups))
	for i, g := range groups {
		out[i] = utls.KeyShare{Group: utls.CurveID(g)}
	}
	return out
}

func ecPoints(in []uint8) []uint8 {
	if len(in) == 0 {
		return []uint8{0}
	}
	return append([]uint8(nil), in...)
}

func alpns(in []string) []string {
	if len(in) == 0 {
		return []string{"http/1.1"}
	}
	return append([]string(nil), in...)
}

func pskModes(in []uint8) []uint8 {
	if len(in) == 0 {
		return []uint8{1}
	}
	return append([]uint8(nil), in...)
}

func paddingLen(n int) int {
	if n <= 0 {
		return 41
	}
	return n
}

func minUint16(in []uint16) uint16 {
	min := in[0]
	for _, v := range in[1:] {
		if v < min {
			min = v
		}
	}
	return min
}

func maxUint16(in []uint16) uint16 {
	max := in[0]
	for _, v := range in[1:] {
		if v > max {
			max = v
		}
	}
	return max
}

// dialRaw 建立 uTLS 握手【之下】的原始 TCP 连接：配置了 ProxyDialer 时经代理
// 拨号,否则走直连 NetDialer。
func (d *UtlsDialer) dialRaw(ctx context.Context, network, addr string) (net.Conn, error) {
	if d.ProxyDialer != nil {
		return d.ProxyDialer(ctx, network, addr)
	}
	return d.NetDialer.DialContext(ctx, network, addr)
}

// WithProxy 返回一个新的 RoundTripper：上游 TCP 连接经 proxyURL 拨号(在 uTLS
// 握手之下),从而出口 IP 走代理、JA3 仍是伪装指纹(PROXY-02a)。实现
// provider.WrapTransportWithProxy 消费的结构化 proxy-aware 接口,无需 provider
// 反向 import 本包。proxyURL scheme 不支持时返回 error,调用方据此 fail-loud。
func (rt *roundTripper) WithProxy(proxyURL *url.URL) (http.RoundTripper, error) {
	pd, err := proxyDialerFromURL(proxyURL)
	if err != nil {
		return nil, err
	}
	dialer := NewUtlsDialer(rt.template)
	dialer.ProxyDialer = pd
	return &roundTripper{
		inner: &http.Transport{
			DialContext:    pd,
			DialTLSContext: dialer.DialTLS,
			// 同直连路径:uTLS UConn 接不住 Go 内置 h2,显式 false 去含糊。
			ForceAttemptHTTP2:     false,
			MaxIdleConns:          256,
			MaxIdleConnsPerHost:   64, // DM-17
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
		template: rt.template,
	}, nil
}

// clientHelloIDForPreset 把内置浏览器 preset 名映射到 uTLS ClientHelloID。uTLS
// 生成真实当前浏览器 ClientHello (不手写/伪造 cipher 数组)。空/未知 -> (_, false)。
// Chrome preset 使用 uTLS 的自动 Chrome ClientHello，随库内置指纹演进。
func clientHelloIDForPreset(preset string) (utls.ClientHelloID, bool) {
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "chrome":
		return utls.HelloChrome_Auto, true
	case "firefox":
		return utls.HelloFirefox_Auto, true
	case "safari":
		return utls.HelloSafari_Auto, true
	case "edge":
		return utls.HelloEdge_Auto, true
	case "ios":
		return utls.HelloIOS_Auto, true
	default:
		return utls.ClientHelloID{}, false
	}
}
