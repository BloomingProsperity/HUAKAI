// upstream_dispatcher.go — 把 vendor adapter / transport.Factory / HTTP
// 客户端拼成一次出站调用。chat_completions_handler 调它替代既有 mock。
//
// 责任：
//  1. 按 ProtocolFamily 选对应 provider.Adapter
//  2. 调 Adapter.BuildRequest 构造 *http.Request
//  3. 按 (provider, mode) 从 transport.Factory 取 RoundTripper
//  4. 发出请求，拿 response.Body 给 forwarder 消费
//  5. 不解析 body / 不消费 stream / 不计费 — 这些由 forwarder + proto
//     adapter 负责
//
// 故意保持薄：所有 vendor-specific 行为收敛到 Adapter；所有 transport-
// specific 行为收敛到 transport.Factory；本文件只做组装。
package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

// AdapterRegistry 按 ProtocolFamily 取对应的 vendor adapter。
type AdapterRegistry interface {
	// For 返回 protocolFamily 对应的 adapter。常见值：
	//   "openai_chat" / "openai_responses" / "anthropic_messages" /
	//   "gemini_messages" / "antigravity" / "cursor" / "copilot" /
	//   "kiro" / "windsurf" / "bedrock_invoke"
	// 未注册时返回 provider.ErrAdapterNotRegistered（或 wrap 它）。
	For(protocolFamily string) (provider.Adapter, error)
}

// DispatchInput 是 Dispatch 的入参。
type DispatchInput struct {
	// ProtocolFamily 决定选哪个 adapter。
	ProtocolFamily string
	// UpstreamModelID 上游真实 model id（registry 解析后）。
	UpstreamModelID string
	// InboundBody 客户原始请求 body 字节。
	InboundBody []byte
	// Account 池中选中 account 摘要。
	Account provider.AccountInfo
	// Credential 出站凭据。
	Credential provider.Credential
	// TransportMode 决定走 standard / mimicry / diagnostics RoundTripper。
	// 零值 ("") 视为 TransportModeStandard。
	TransportMode transport.TransportMode
}

// DispatchResult 是 Dispatch 的产出。调用方读完 UpstreamReader 后必须
// 调 Close()。
type DispatchResult struct {
	// UpstreamReader 上游响应 body（即 response.Body）；交给
	// StreamForwarder.Forward 消费。
	UpstreamReader io.Reader
	// StatusCode 上游 HTTP 状态码（200 / 401 / 429 / 503 等）。
	StatusCode int
	// Headers 上游响应 headers（用于 R6 错误归一化与 R8 outbound
	// allowlist）。
	Headers http.Header
	// Close 关闭上游 response.Body 的 hook。调用方读完必调。
	Close func() error
}

// HTTPDoer 是 http.Client 的最小子集，便于注入 mock。
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// UpstreamDispatcher 串起 adapter / transport / HTTP client。
type UpstreamDispatcher struct {
	// Adapters 按 protocol family 取 adapter；必须非 nil。
	Adapters AdapterRegistry
	// TransportFactory 按 provider/mode 取 RoundTripper；必须非 nil。
	TransportFactory *transport.Factory
	// ProtocolAdapters 按 protocol family 取 HCSF upstream adapter；nil 时
	// DispatchHCSF 使用默认注册表。raw Dispatch 不读取本字段。
	ProtocolAdapters ProtocolAdapterRegistry
	// HTTPClient 用于 Do() 调用。空值时按 transport 选好的 RoundTripper
	// 现场构造一个 http.Client。注入便于测试。
	HTTPClient HTTPDoer
	// ProxyResolver 可选：按 accountID 解析出站代理。nil 表示全部走直连。
	// 未注册 account（ErrAccountNotFound）= 直连，不视为错误。
	// 仅在 HTTPClient 为 nil 的生产路径生效。
	ProxyResolver provider.ProxyResolver
}

// Dispatch 执行一次完整出站。失败时 result 可能为 nil；调用方按 err
// 与 status 决定是否重试 / fallback。
func (d *UpstreamDispatcher) Dispatch(ctx context.Context, in DispatchInput) (*DispatchResult, error) {
	if d == nil {
		return nil, errors.New("dispatcher: nil receiver")
	}
	if d.Adapters == nil {
		return nil, errors.New("dispatcher: AdapterRegistry 未配置")
	}
	if d.TransportFactory == nil {
		return nil, errors.New("dispatcher: TransportFactory 未配置")
	}

	// 1. 选 adapter
	adapter, err := d.Adapters.For(in.ProtocolFamily)
	if err != nil {
		return nil, fmt.Errorf("dispatcher: 取 adapter 失败 (protocol=%q): %w", in.ProtocolFamily, err)
	}

	// 2. 构造出站请求
	req, err := adapter.BuildRequest(ctx, provider.BuildInput{
		UpstreamModelID: in.UpstreamModelID,
		InboundBody:     in.InboundBody,
		Credential:      in.Credential,
		Account:         in.Account,
	})
	if err != nil {
		return nil, fmt.Errorf("dispatcher: BuildRequest 失败: %w", err)
	}
	if err := validatePassthroughEndpointTarget(ctx, in.Credential, req); err != nil {
		return nil, err
	}

	// 3. 取 transport
	mode := in.TransportMode
	if mode == "" {
		mode = transport.TransportModeStandard
	}
	rt, err := d.TransportFactory.For(transport.ProviderCode(in.Account.Platform), mode)
	if err != nil {
		return nil, fmt.Errorf("dispatcher: 取 RoundTripper 失败: %w", err)
	}

	// 4. 发出请求
	client := d.HTTPClient
	if client == nil {
		rt, err = d.applyProxy(ctx, rt, in.Account.AccountID)
		if err != nil {
			return nil, err
		}
		if provider.UsesCustomPassthroughEndpoint(in.Credential) {
			rt, err = provider.WrapPassthroughEndpointTransport(rt)
			if err != nil {
				return nil, fmt.Errorf("dispatcher: passthrough endpoint rejected: %w", err)
			}
		}
		client = &http.Client{Transport: rt}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dispatcher: HTTP Do 失败: %w", err)
	}

	// 5. 组装结果
	return &DispatchResult{
		UpstreamReader: resp.Body,
		StatusCode:     resp.StatusCode,
		Headers:        resp.Header,
		Close:          resp.Body.Close,
	}, nil
}

// applyProxy 按 ProxyResolver 决定是否给 rt 包上代理。
// 返回 (wrapped_rt, err)。下列情况返回原 rt + nil err：
//   - ProxyResolver 未配置（nil）
//   - accountID == 0（无账号信息，无法解析）
//   - resolver 返回 ErrAccountNotFound（未注册 = 直连，非错误）
//   - resolver 返回 nil URL（已注册但明确直连）
//
// 仅在 resolver 返回非 NotFound 错误时返回 (nil, err)。特别地，
// ErrProxyResolverMisconfigured（DI / 配置错误）会**直接传播**，
// 不会 fail-open 到直连——否则 misconfig 会让所有账号绕过代理，破坏
// 账号级 IP 隔离。
func (d *UpstreamDispatcher) applyProxy(ctx context.Context, rt http.RoundTripper, accountID int64) (http.RoundTripper, error) {
	if d.ProxyResolver == nil || accountID == 0 {
		return rt, nil
	}
	proxyURL, err := d.ProxyResolver.Resolve(ctx, accountID)
	if errors.Is(err, provider.ErrAccountNotFound) {
		return rt, nil
	}
	if err != nil {
		return nil, fmt.Errorf("dispatcher: ProxyResolver.Resolve 失败: %w", err)
	}
	return provider.WrapTransportWithProxy(rt, proxyURL), nil
}

func validatePassthroughEndpointTarget(ctx context.Context, cred provider.Credential, req *http.Request) error {
	if !provider.UsesCustomPassthroughEndpoint(cred) {
		return nil
	}
	if req == nil {
		return fmt.Errorf("dispatcher: passthrough endpoint rejected: %w", provider.ErrUnsafePassthroughEndpoint)
	}
	if err := provider.ValidatePassthroughEndpointTarget(ctx, req.URL); err != nil {
		return fmt.Errorf("dispatcher: passthrough endpoint rejected: %w", err)
	}
	return nil
}
