package proxyhealth

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/url"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/ssrfpolicy"
	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

// DefaultProbeCanary 是 probe-through 的**服务端常量**探测目标(稳定、低风险的 TLS 端点)。
// admin **绝不能**指定探测目标——固定常量是杜绝"代理隧道 + 任意目标 = 双跳 SSRF"的核心守卫。
const DefaultProbeCanary = "api.anthropic.com:443"

// probe-through 错误分类(粗粒度枚举;绝不回传原始错误,防泄露内网拓扑/凭据片段)。
const (
	ErrClassUnsafeProxyHost = "unsafe_proxy_host" // 代理 host 指向内网/metadata,拒绝主动拨号(SSRF 守卫①)
	ErrClassTargetDenied    = "target_denied"     // canary 未过 SSRF 策略(不该发生,常量已白名单)
	ErrClassBadProxyURL     = "bad_proxy_url"      // 代理 URL 无法构造 dialer(scheme 不支持等)
	ErrClassDialTimeout     = "dial_timeout"       // 经代理建隧道超时
	ErrClassTunnelRefused   = "tunnel_refused"     // 代理拒绝/隧道建立失败
	ErrClassTLSFail         = "tls_fail"           // 隧道通但到 canary 的 TLS 握手失败
)

// ProbeResult 是一次主动 probe-through 的结果。**绝不含代理 URL/凭据/原始错误**。
type ProbeResult struct {
	OK         bool
	LatencyMS  int64
	ErrorClass string // OK 时为空;否则为上面的枚举之一
}

// ProbeThrough 经 proxyURL 指定的代理建隧道到 canary(host:port),测真实出站连通性 + 延迟,
// 并做一次到 canary 的 TLS 握手(证代理能承载 TLS 出站,而非仅 TCP 接通)。
//
// SSRF 守卫②:canary 必须由调用方传入**服务端常量**,且本函数仍用 policy.AllowsHost 复校其 host
// (default-deny 心智:即便常量也过策略)。⚠ proxyURL 含凭据 → 本函数绝不记录/返回它,只喂给 dialer;
// 返回结果只有 {ok, latency_ms, error_class},不含任何代理/目标细节。
func ProbeThrough(ctx context.Context, policy ssrfpolicy.Policy, proxyURL *url.URL, canary string) ProbeResult {
	host, _, err := net.SplitHostPort(canary)
	if err != nil || host == "" || !policy.AllowsHost(host) {
		return ProbeResult{OK: false, ErrorClass: ErrClassTargetDenied}
	}
	dialer, err := mimicry.DialerFromURL(proxyURL)
	if err != nil {
		return ProbeResult{OK: false, ErrorClass: ErrClassBadProxyURL}
	}

	start := time.Now()
	conn, err := dialer(ctx, "tcp", canary)
	if err != nil {
		return ProbeResult{OK: false, LatencyMS: time.Since(start).Milliseconds(), ErrorClass: classifyDialErr(err)}
	}
	defer conn.Close()

	tconn := tls.Client(conn, &tls.Config{ServerName: host})
	hsCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := tconn.HandshakeContext(hsCtx); err != nil {
		return ProbeResult{OK: false, LatencyMS: time.Since(start).Milliseconds(), ErrorClass: ErrClassTLSFail}
	}
	_ = tconn.Close()
	return ProbeResult{OK: true, LatencyMS: time.Since(start).Milliseconds()}
}

func classifyDialErr(err error) string {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return ErrClassDialTimeout
	}
	return ErrClassTunnelRefused
}
