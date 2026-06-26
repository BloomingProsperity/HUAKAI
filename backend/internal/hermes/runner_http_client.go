package hermes

import (
	"net"
	"net/http"
	"time"
)

// runner 出口的各项边界。这些故意不包含总的
// Client.Timeout：RunnerClient.Chat 返回一个长生命周期的 Server-Sent-Events
// 流，chat bridge 会增量读取，而一刀切的 deadline 会在 token 中途
// 截断一个健康的流。因此我们只对一个生病或不可达的 runner
// 可能卡住的阶段设界——连接、TLS 握手以及
// 等待响应头——这样流式 body 本身保持无界
//（由入站请求的 context 管控）。
const (
	// runnerConnectTimeout 限制拨号时间。一个 DOWN 的 runner（拒绝/黑洞）
	// 会在数秒内失败,而不是挂起到入站 deadline。
	runnerConnectTimeout = 5 * time.Second
	// runnerTLSHandshakeTimeout 对挂死的 TLS 对端限制 TLS 握手时间。
	runnerTLSHandshakeTimeout = 5 * time.Second
	// runnerResponseHeaderTimeout 限制从完成请求写出
	// 到收到响应头之间的等待时间。一个 SSE runner 会在生成 token 之前
	// 先写出它的 200 + text/event-stream 头,所以这能对挂死的
	// runner 设界,而不会切断一个慢但仍工作的流的 body。保持在
	// 60s 控制面 deadline 之下,使它在坏路径上成为更紧的边界。
	runnerResponseHeaderTimeout = 50 * time.Second
	runnerExpectContinueTimeout = 1 * time.Second
	runnerIdleConnTimeout       = 90 * time.Second
	runnerMaxIdleConns          = 64
	runnerMaxIdleConnsPerHost   = 16
)

// defaultRunnerHTTPClient 返回生产中用于 runner 出口的有界 HTTP client。
// 没有它,client 会回退到 http.DefaultClient,后者
// 没有连接/TLS/响应头的边界:一波针对
// 已死或挂死 runner 的 admin chat 会各自占住一个 goroutine(以及 SSE 读取),
// 直到入站 60s deadline,堆积在与核心数据面共享的资源上。
// 有界 client 会让坏 runner 转为快速失败。
func defaultRunnerHTTPClient() *http.Client {
	return &http.Client{
		// 不设 Timeout:流式响应绝不能被截断。见上面的 const 块。
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: runnerConnectTimeout, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   runnerTLSHandshakeTimeout,
			ResponseHeaderTimeout: runnerResponseHeaderTimeout,
			ExpectContinueTimeout: runnerExpectContinueTimeout,
			IdleConnTimeout:       runnerIdleConnTimeout,
			MaxIdleConns:          runnerMaxIdleConns,
			MaxIdleConnsPerHost:   runnerMaxIdleConnsPerHost,
			ForceAttemptHTTP2:     true,
		},
	}
}
