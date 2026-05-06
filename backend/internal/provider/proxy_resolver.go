// proxy_resolver.go — per-account 出站代理路由。
//
// ProxyResolver 接口允许 dispatcher 按账号 ID 解析出站代理 URL，与全局
// 共享 transport 解耦，实现账号级 IP 隔离。常用于：
//   - 不同账号绑不同代理避免 rate-limit / IP 信誉污染
//   - 海外 vendor 路径走 SOCKS5 proxy 出站
//   - 测试场景下账号级 IP 关联
//
// 合规中性 — 用代理是合法的，与 R3 transport mimicry（部分暂停）解耦。
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

// WrapTransportWithProxy 把代理 URL 注入到 RoundTripper：
//   - proxyURL == nil → 返回原 rt 不变（零开销直连）
//   - rt 是 *http.Transport → Clone() 浅拷贝并设 Proxy func（保留连接池
//     参数，不影响原实例）
//   - 其它 rt（utls dialer / 测试 mock 等） → wrap 为 proxyWrappedRoundTripper
//     注意：非 *http.Transport 的 wrap 仅传播 proxy 意图给查询方；实际
//     代理隧道由内层 RoundTripper 自行处理（utls 实施时按需扩展）。
func WrapTransportWithProxy(rt http.RoundTripper, proxyURL *url.URL) http.RoundTripper {
	if proxyURL == nil {
		return rt
	}
	if t, ok := rt.(*http.Transport); ok {
		clone := t.Clone()
		clone.Proxy = http.ProxyURL(proxyURL)
		return clone
	}
	return &proxyWrappedRoundTripper{inner: rt, proxyURL: proxyURL}
}

// proxyWrappedRoundTripper 包装任意 RoundTripper，记录代理 URL 供上层查询。
type proxyWrappedRoundTripper struct {
	inner    http.RoundTripper
	proxyURL *url.URL
}

func (p *proxyWrappedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return p.inner.RoundTrip(req)
}

// ProxyURL 返回包装的代理 URL，供测试断言。
func (p *proxyWrappedRoundTripper) ProxyURL() *url.URL {
	return p.proxyURL
}
