// proxy_resolver.go — per-account 出站代理路由。
//
// ProxyResolver 接口允许 dispatcher 按账号 ID 解析出站代理 URL，与全局
// 共享 transport 解耦，实现账号级 IP 隔离。常用于：
//   - 不同账号绑不同代理避免 rate-limit / IP 信誉污染
//   - 海外 vendor 路径走 SOCKS5 proxy 出站
//   - 测试场景下账号级 IP 关联
//
// 合规中性 — 用代理是合法的，与 transport mimicry 解耦。
//
// 复用既有 ErrAccountNotFound（声明在 vault.go）：vault Resolve 与
// proxy Resolve 共享"未找到 account"语义。
package provider

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"
)

// ProxyResolver 按账号 ID 解析代理 URL。
//
// Resolve 返回值语义：
//   - (*url.URL, nil)：成功，非 nil URL 表示使用该代理，nil URL 表示
//     直连（已明确配置无代理）
//   - (nil, ErrAccountNotFound)：账号未在 resolver 中注册（生产路径视为直连）
//   - (nil, ErrProxyResolverMisconfigured)：resolver 自身 nil / pool nil
//     等基础设施错误（**不能**当成 ErrAccountNotFound 处理，否则
//     misconfig 会让所有账号 fail-open 绕过代理 → 破坏账号级 IP 隔离）
//   - (nil, other error)：其他解析错误
type ProxyResolver interface {
	Resolve(ctx context.Context, accountID int64) (*url.URL, error)
}

// ErrProxyResolverMisconfigured 表示 ProxyResolver 自身基础设施错误
// （nil receiver / nil pool / 未初始化）。与 ErrAccountNotFound 严格区分：
// 后者是"账号无代理偏好 → 直连"，前者是"resolver 不可用 → 必须 fail-loud
// 让 dispatcher 拒绝放行请求"。
var ErrProxyResolverMisconfigured = errors.New("provider: proxy resolver misconfigured")

// ErrProxyUnsupportedTransport 表示账号已配置代理，但 RoundTripper 不能安全
// 注入代理。这里必须 fail-loud，避免账号级 IP 隔离被静默旁路。
var ErrProxyUnsupportedTransport = errors.New("provider: proxy unsupported for non-standard transport")

// StaticProxyResolver 是基于内存 map 的静态代理解析器。线程安全。
type StaticProxyResolver struct {
	mu      sync.RWMutex
	entries map[int64]*url.URL // key=accountID，value=代理URL（nil 表示直连）
}

// NewStaticProxyResolver 创建空 resolver。
func NewStaticProxyResolver() *StaticProxyResolver {
	return &StaticProxyResolver{entries: make(map[int64]*url.URL)}
}

// Set 注册账号的代理配置。proxyURL 为 nil 时表示该账号明确直连
// （与未注册区分）。accountID==0 视为无效。nil receiver 也明确报错。
func (r *StaticProxyResolver) Set(accountID int64, proxyURL *url.URL) error {
	if r == nil {
		return errProxyNilReceiver
	}
	if accountID == 0 {
		return errProxyInvalidAccountID
	}
	r.mu.Lock()
	r.entries[accountID] = proxyURL
	r.mu.Unlock()
	return nil
}

// Resolve 按 accountID 返回代理 URL。
// nil URL + nil error 表示已注册且直连；ErrAccountNotFound 表示未注册；
// ErrProxyResolverMisconfigured 表示 resolver 自身 nil（DI 错误，必须 fail-loud）。
func (r *StaticProxyResolver) Resolve(_ context.Context, accountID int64) (*url.URL, error) {
	if r == nil {
		return nil, ErrProxyResolverMisconfigured
	}
	r.mu.RLock()
	proxyURL, ok := r.entries[accountID]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrAccountNotFound
	}
	return proxyURL, nil
}

// Size 返回已注册账号数。
func (r *StaticProxyResolver) Size() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}

var _ ProxyResolver = (*StaticProxyResolver)(nil)

// errProxyInvalidAccountID 是 Set 拒绝 accountID==0 时的本地错误。
var errProxyInvalidAccountID = simpleError("provider: proxy resolver Set 拒绝 accountID==0")

// errProxyNilReceiver 是 Set 调用方传入 nil receiver 时的本地错误。
var errProxyNilReceiver = simpleError("provider: proxy resolver Set 调用了 nil receiver")

type simpleError string

func (e simpleError) Error() string { return string(e) }

// proxyAwareRoundTripper 是能把账号代理结构化下发到自身出口实现的 RoundTripper。
// 结构化接口 —— 实现方(transport/mimicry)无需被本包 import,避免循环依赖。
type proxyAwareRoundTripper interface {
	WithProxy(*url.URL) (http.RoundTripper, error)
}

// WrapTransportWithProxy 把代理 URL 注入到 RoundTripper：
//   - proxyURL == nil → 返回原 rt 不变（零开销直连）
//   - rt 是 *http.Transport → Clone() 浅拷贝并设 Proxy func（保留连接池
//     参数，不影响原实例）
//   - 其它 rt（Rust sidecar transport / 测试 mock 等） → wrap 为 fail-loud
//     proxyWrappedRoundTripper，避免静默直连绕过账号级代理隔离。
func WrapTransportWithProxy(rt http.RoundTripper, proxyURL *url.URL) http.RoundTripper {
	if proxyURL == nil {
		return rt
	}
	if _, _, err := validateProxyEndpointURL(proxyURL); err != nil {
		return &proxyWrappedRoundTripper{inner: rt, proxyURL: proxyURL, buildErr: err}
	}
	if pa, ok := rt.(proxyAwareRoundTripper); ok {
		wrapped, err := pa.WithProxy(proxyURL)
		if err != nil {
			return &proxyWrappedRoundTripper{inner: rt, proxyURL: proxyURL, buildErr: err}
		}
		return wrapped
	}
	if t, ok := rt.(*http.Transport); ok {
		if t.DialTLSContext != nil {
			// 该 transport 用 DialTLSContext 自管 TLS 连接(如 mimicry sidecar 路),
			// Go 的 net/http 在这种情况下【不消费】Proxy func —— Clone+设 Proxy 会
			// 静默丢弃代理,真实出口 IP 泄露,破坏账号级 IP 隔离。fail-loud
			// (ErrProxyUnsupportedTransport)而非静默泄露。让 sidecar+代理真正可用
			// 需把代理穿进 Rust 控制帧(PROXY-02b)。
			return &proxyWrappedRoundTripper{inner: rt, proxyURL: proxyURL}
		}
		wrapped, err := wrapProxyEndpointTransport(t, proxyURL)
		if err != nil {
			return &proxyWrappedRoundTripper{inner: rt, proxyURL: proxyURL, buildErr: err}
		}
		return wrapped
	}
	return &proxyWrappedRoundTripper{inner: rt, proxyURL: proxyURL}
}

// proxyWrappedRoundTripper 包装任意 RoundTripper，记录代理 URL 供上层查询。
type proxyWrappedRoundTripper struct {
	inner    http.RoundTripper
	proxyURL *url.URL
	buildErr error
}

func (p *proxyWrappedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if p.buildErr != nil {
		return nil, p.buildErr
	}
	return nil, ErrProxyUnsupportedTransport
}

// ProxyURL 返回包装的代理 URL，供测试断言。
func (p *proxyWrappedRoundTripper) ProxyURL() *url.URL {
	return p.proxyURL
}
