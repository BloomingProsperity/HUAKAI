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
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/cacheplan"
	"github.com/BloomingProsperity/HUAKAI/internal/headerfirewall"
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
	// EndpointPath 可选覆盖 adapter 默认 endpoint path。空值保持既有
	// protocol family 默认 endpoint；/v1/embeddings 等 OpenAI-compatible
	// passthrough 端点可在不新增 protocol family 的情况下指定。
	EndpointPath string
	// UpstreamModelID 上游真实 model id（registry 解析后）。
	UpstreamModelID string
	// InboundBody 客户原始请求 body 字节。
	InboundBody []byte
	// BodyControls are optional per-channel pre-dispatch JSON transforms.
	// Zero value is a no-op.
	BodyControls DispatchBodyControls
	// InboundContentType 是入口请求 Content-Type。空值保持 adapter 默认；
	// multipart audio 透传时必须带原 boundary。
	InboundContentType string
	// InboundBetaTokens 客户端 anthropic-beta 请求头解析出的 token 列表
	// (provider.ParseInboundBetaTokens 产出),原样穿给 provider.BuildInput;
	// 仅 anthropic 族 adapter 消费,其余族忽略。
	InboundBetaTokens []string
	// Account 池中选中 account 摘要。
	Account provider.AccountInfo
	// Credential 出站凭据。
	Credential provider.Credential
	// TransportMode 决定走 standard / mimicry / diagnostics RoundTripper。
	// 零值 ("") 视为 TransportModeStandard。
	TransportMode transport.TransportMode
	// NonStreamingBuffered enables the non-streaming outbound hard timeouts.
	// Streaming callers leave this false so stream-specific timeout axes stay
	// owned by StreamForwarder.
	NonStreamingBuffered bool
	// ClientStreamIntent 客户端流式意图,原样穿给 provider.BuildInput(语义见
	// 彼处注释)。gemini-shaped 族跨协议流式的端点选择依赖它。
	ClientStreamIntent bool
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
	// TLSProfileResolver 可选:按 accountID 解析账号绑定的 DB TLS-fingerprint
	// profile -> per-account uTLS RoundTripper。nil 返回 = 保持 builtin per-mode
	// 指纹(未绑定/非 active/profile 非法)。仅 mimicry mode + HTTPClient 为 nil
	// 的生产路径生效。
	TLSProfileResolver TLSProfileResolver
	// Timeouts applies only to non-streaming buffered dispatches.
	Timeouts TimeoutConfig
	// AnthropicAutoBreakpoints opts into automatic cache_control breakpoint
	// planning on the live Anthropic Messages egress path. Default false keeps
	// the body byte-for-byte. When true, a request whose protocol family is
	// "anthropic_messages" AND that carries no client-supplied cache_control
	// gets ephemeral breakpoints injected at planner-chosen positions just
	// before BuildRequest. A client that brings its own cache_control is never
	// touched. Any planning/serialization error is swallowed and the original
	// body is used unchanged — caching is an optimization, never a hard
	// dependency of a live request.
	AnthropicAutoBreakpoints bool
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

	controlledBody, err := ApplyDispatchBodyControls(in.InboundBody, in.BodyControls)
	if err != nil {
		return nil, fmt.Errorf("dispatcher: channel request controls 失败: %w", err)
	}
	in.InboundBody = controlledBody

	// RR-04: trim client-supplied excess cache_control breakpoints to
	// CacheControlMaxAllowed before forwarding. Anthropic 400s requests
	// with >4 cache_control blocks. Fail-open: on decode error the body
	// is forwarded unchanged.
	if trimmed, _ := EnforceCacheControlLimit(in.InboundBody, CacheControlMaxAllowed); len(trimmed) > 0 {
		in.InboundBody = trimmed
	}

	// 1.5 Optional Anthropic cache_control breakpoint planning. Replaces the
	// local inbound body only for the anthropic_messages family and only when
	// opted in; see maybeInjectAnthropicBreakpoints.
	in.InboundBody = d.maybeInjectAnthropicBreakpoints(in.ProtocolFamily, in.InboundBody)

	// 2. 构造出站请求
	req, err := adapter.BuildRequest(ctx, provider.BuildInput{
		UpstreamModelID:    in.UpstreamModelID,
		InboundBody:        in.InboundBody,
		InboundContentType: in.InboundContentType,
		Credential:         in.Credential,
		Account:            in.Account,
		EndpointPath:       in.EndpointPath,
		InboundBetaTokens:  in.InboundBetaTokens,
		ClientStreamIntent: in.ClientStreamIntent,
	})
	if err != nil {
		return nil, fmt.Errorf("dispatcher: BuildRequest 失败: %w", err)
	}
	if err := validatePassthroughEndpointTarget(ctx, in.Credential, req); err != nil {
		return nil, err
	}
	headerfirewall.StripHopByHopRequestHeaders(req.Header)
	headerfirewall.NormalizeEgressRequestHeaders(req.Header)

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
		rt = d.applyTLSProfile(ctx, rt, mode, in.Account.AccountID)
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
		client = d.httpClientForRoundTripper(rt, in.NonStreamingBuffered)
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

func (d *UpstreamDispatcher) httpClientForRoundTripper(rt http.RoundTripper, nonStreaming bool) *http.Client {
	if !nonStreaming {
		return &http.Client{Transport: rt}
	}
	cfg := d.Timeouts
	if cfg.HeaderToFirstByte > 0 {
		rt = roundTripperWithResponseHeaderTimeout(rt, cfg.HeaderToFirstByte)
	}
	client := &http.Client{Transport: rt}
	if cfg.RequestTotalTimeout > 0 {
		client.Timeout = cfg.RequestTotalTimeout
	}
	return client
}

func roundTripperWithResponseHeaderTimeout(rt http.RoundTripper, timeout time.Duration) http.RoundTripper {
	if timeout <= 0 {
		return rt
	}
	if t, ok := rt.(*http.Transport); ok {
		clone := t.Clone()
		clone.ResponseHeaderTimeout = timeout
		return clone
	}
	return rt
}

// TLSProfileResolver 按账号解析绑定的 DB TLS-fingerprint profile,返回驱动该
// profile 指纹的 uTLS RoundTripper;nil = 保持 builtin。结构化接口,实现在
// internal/tlsfpresolve(gateway 无需反向 import)。
type TLSProfileResolver interface {
	ResolveRoundTripper(ctx context.Context, accountID int64) (http.RoundTripper, error)
}

// applyTLSProfile 在 mimicry mode 下用账号绑定的 DB TLS profile RT 替换 rt;
// 无绑定/非 mimicry/解析失败一律保持原 rt(builtin)。永不让 dispatch 失败 ——
// 返回的 profile RT 实现 proxy-aware WithProxy,故 applyProxy 仍能正确叠加代理。
func (d *UpstreamDispatcher) applyTLSProfile(ctx context.Context, rt http.RoundTripper, mode transport.TransportMode, accountID int64) http.RoundTripper {
	// 全局伪装关闭时(HUAKAI_TRANSPORT_MIMICRY=false),DB profile 旁路也必须跳过:
	// 否则绑定了 DB TLS profile 的账号会在 For 已把 mode 降级标准 transport 之后,
	// 又被这里重新换上 uTLS profile RT,留下伪装死角。复用 transport.MimicryEnabled
	// 保证与 factory.For 的开关判定完全一致。
	if d.TLSProfileResolver == nil || accountID == 0 || mode == transport.TransportModeStandard || !transport.MimicryEnabled() {
		return rt
	}
	// 收编 DB TLS profile 旁路:若传入的 rt 已经是走 Rust tls-sidecar 的 RT(自带内置
	// 真指纹),绝不能用 per-account DB uTLS profile 整体替换它——否则绑定 DB profile 的
	// 账号会让 sidecar 永远轮不到用、退回 Go uTLS 占位指纹。sidecar RT 通过 SidecarProfileID()
	// 自证(仅 mimicry.sidecarRoundTripper 实现该方法,检测精确,不会误伤非 sidecar RT)。
	if _, ok := rt.(interface{ SidecarProfileID() string }); ok {
		return rt
	}
	if profileRT, err := d.TLSProfileResolver.ResolveRoundTripper(ctx, accountID); err == nil && profileRT != nil {
		return profileRT
	}
	return rt
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

// maybeInjectAnthropicBreakpoints plans and applies ephemeral cache_control
// breakpoints on an Anthropic Messages request body just before it is built
// into an outbound HTTP request. It is a no-op (returns body unchanged) when
// any of these hold:
//   - AnthropicAutoBreakpoints is not enabled on the dispatcher;
//   - the protocol family is not "anthropic_messages";
//   - the client already supplied at least one cache_control field anywhere
//     in the request (system / message content / tools) — we never override
//     a client that manages its own caching;
//   - the planner produces no positions, or any inspect/plan/apply step
//     errors. Caching is an optimization; a planning failure must never
//     break a live request, so on error the original body is returned.
//
// The returned slice is either the original body (untouched) or a freshly
// allocated, re-serialized body from ApplyBreakpoints; the caller's slice is
// never mutated in place.
func (d *UpstreamDispatcher) maybeInjectAnthropicBreakpoints(protocolFamily string, body []byte) []byte {
	if d == nil || !d.AnthropicAutoBreakpoints {
		return body
	}
	if protocolFamily != "anthropic_messages" {
		return body
	}
	if len(body) == 0 {
		return body
	}
	// Client already brought its own cache_control: leave the body verbatim.
	if cacheplan.HasAnyCacheControl(body) {
		return body
	}
	snapshot, err := InspectCacheControl(body)
	if err != nil {
		return body
	}
	suggestion, err := SuggestBreakpoints(body, snapshot, nil)
	if err != nil || len(suggestion.Add) == 0 {
		return body
	}
	result, err := ApplyBreakpoints(body, suggestion)
	if err != nil || len(result.Applied) == 0 || len(result.Body) == 0 {
		return body
	}
	return result.Body
}
