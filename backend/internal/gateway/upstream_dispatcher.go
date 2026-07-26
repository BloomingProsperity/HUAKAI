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
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/cachecontrol"
	"github.com/BloomingProsperity/HUAKAI/internal/cacheplan"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway/streamusage"
	"github.com/BloomingProsperity/HUAKAI/internal/headerfirewall"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
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
	// HTTPMethod 可选覆盖支持该能力的 adapter 默认方法；空值保持默认 POST。
	HTTPMethod string
	// ProtocolFamily 决定选哪个 adapter。
	ProtocolFamily string
	// EndpointPath 可选覆盖 adapter 默认 endpoint path。空值保持既有
	// protocol family 默认 endpoint；/v1/embeddings 等 OpenAI-compatible
	// passthrough 端点可在不新增 protocol family 的情况下指定。
	EndpointPath string
	// EndpointQuery 是内部生成的结构化查询串，不接受客户端原始 query。
	// 账号级目录分页等控制面请求用它保持 path 与 query 的边界。
	EndpointQuery string
	// UpstreamModelID 上游真实 model id（registry 解析后）。
	UpstreamModelID string
	// InboundBody 客户原始请求 body 字节。
	InboundBody []byte
	// BodyControls 是可选的 per-channel 出站前 JSON 变换。
	// 零值为空操作。
	BodyControls DispatchBodyControls
	// InboundContentType 是入口请求 Content-Type。空值保持 adapter 默认；
	// multipart audio 透传时必须带原 boundary。
	InboundContentType string
	// IdempotencyKey 是网关自身为可重试的上游创建操作生成的稳定幂等键。
	// 它不是客户端任意请求头透传；只有明确需要的内部调用方才能设置。
	IdempotencyKey string
	// InboundBetaTokens 客户端 anthropic-beta 请求头解析出的 token 列表
	// (provider.ParseInboundBetaTokens 产出),原样穿给 provider.BuildInput;
	// 仅 anthropic 族 adapter 消费,其余族忽略。
	InboundBetaTokens []string
	// Account 池中选中 account 摘要。
	Account provider.AccountInfo
	// Credential 出站凭据。
	Credential provider.Credential
	// TransportMode 决定走 standard / mimicry / diagnostics RoundTripper。
	// 零值 ("") 按 ProtocolFamily、Account.Platform 与 Account.AccountType
	// 自动选择；显式 standard 可强制普通出口。
	TransportMode transport.TransportMode
	// NonStreamingBuffered 启用非流式出站的硬超时。
	// 流式调用方保持其为 false,使流式专属的超时维度仍由
	// StreamForwarder 掌管。
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

// DispatchOutcomeUnknownError 表示 HTTP 请求已经交给 Do 执行，但调用方没有拿到
// 可判定的上游响应。此时请求可能尚未送达，也可能已经产生外部副作用；上层不得把它
// 当成“确定未发送”自动重提创建类请求。
type DispatchOutcomeUnknownError struct {
	err error
}

func (e *DispatchOutcomeUnknownError) Error() string {
	if e == nil || e.err == nil {
		return "dispatcher: 请求结果未知"
	}
	return "dispatcher: HTTP 请求结果未知: " + e.err.Error()
}

func (e *DispatchOutcomeUnknownError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func newDispatchOutcomeUnknownError(err error) error {
	if err == nil {
		return nil
	}
	return &DispatchOutcomeUnknownError{err: err}
}

// IsDispatchOutcomeUnknown 供有外部副作用的调用方区分“发送前失败”和“可能已发送”。
func IsDispatchOutcomeUnknown(err error) bool {
	var target *DispatchOutcomeUnknownError
	return errors.As(err, &target)
}

type AnthropicTTLSettings interface {
	AnthropicTTL1hRewriteEnabled(context.Context) (bool, error)
}

// ResolveDispatchTransport 统一解析一次账号出站所需的 provider 与 transport
// mode。协议族优先表达订阅/session 出口，账号类型用于兼容同一平台下的多种
// 鉴权形态；公开 API key 保持 standard。
func ResolveDispatchTransport(account provider.AccountInfo, protocolFamily string) (provider.AccountInfo, transport.TransportMode) {
	providerCode := dispatchTransportProvider(account, protocolFamily)
	account.Platform = string(providerCode)
	return account, dispatchTransportMode(providerCode, account.AccountType)
}

func dispatchTransportProvider(account provider.AccountInfo, protocolFamily string) transport.ProviderCode {
	switch strings.ToLower(strings.TrimSpace(protocolFamily)) {
	case "openai_codex":
		return transport.ProviderOpenAICodex
	case "gemini_advanced_session":
		return transport.ProviderGeminiAdvanced
	case "gemini_code_assist":
		return transport.ProviderGeminiCodeAssist
	case "antigravity_session":
		return transport.ProviderAntigravity
	case "cursor_session":
		return transport.ProviderCursor
	case "copilot_session":
		return transport.ProviderCopilot
	case "kiro_session":
		return transport.ProviderKiro
	case "windsurf_session":
		return transport.ProviderWindsurf
	}

	providerCode := transport.ProviderCode(strings.ToLower(strings.TrimSpace(account.Platform)))
	switch providerCode {
	case transport.ProviderOpenAI:
		switch strings.ToLower(strings.TrimSpace(account.AccountType)) {
		case credentialstore.AuthModeChatGPTOAuth, credentialstore.AuthModeCodexCLIOAuth, credentialstore.AuthModeCodexWebOAuth:
			return transport.ProviderOpenAICodex
		}
	case transport.ProviderGemini:
		switch strings.ToLower(strings.TrimSpace(account.AccountType)) {
		case credentialstore.AuthModeGoogleOne:
			return transport.ProviderGeminiAdvanced
		case credentialstore.AuthModeAntigravity:
			return transport.ProviderAntigravity
		}
	}
	return providerCode
}

func dispatchTransportMode(providerCode transport.ProviderCode, accountType string) transport.TransportMode {
	switch providerCode {
	case transport.ProviderOpenAICodex:
		return transport.TransportModeMimicryChatGPT
	case transport.ProviderGeminiAdvanced:
		return transport.TransportModeMimicryGeminiAdvanced
	case transport.ProviderAntigravity:
		// 推理链的统一默认是标准 TLS + H1；自动 Dispatch 在看见模型发现等
		// 控制面路径后会由 dispatchTransportModeForRequest 放宽到标准协商。
		return transport.TransportModeStandardH1
	case transport.ProviderCursor:
		return transport.TransportModeMimicryCursor
	case transport.ProviderCopilot:
		return transport.TransportModeMimicryCopilot
	case transport.ProviderKiro:
		return transport.TransportModeMimicryKiro
	case transport.ProviderWindsurf:
		return transport.TransportModeMimicryWindsurf
	case transport.ProviderAnthropic:
		switch strings.ToLower(strings.TrimSpace(accountType)) {
		case "oauth", "session", credentialstore.AuthModeClaudeAIOAuth, credentialstore.AuthModeClaudeCode,
			credentialstore.AuthModeClaudeSetupToken:
			return transport.TransportModeMimicryClaudeCode
		}
	}
	return transport.TransportModeStandard
}

func dispatchTransportModeForRequest(providerCode transport.ProviderCode, accountType, endpointPath string) transport.TransportMode {
	mode := dispatchTransportMode(providerCode, accountType)
	if providerCode != transport.ProviderAntigravity {
		return mode
	}
	path := strings.ToLower(strings.TrimSpace(endpointPath))
	if strings.Contains(path, ":generatecontent") || strings.Contains(path, ":streamgeneratecontent") {
		return transport.TransportModeStandardH1
	}
	return transport.TransportModeStandard
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
	// TLSProfileResolver 可选：按 accountID 选择并校验数据库动态 TLS profile。
	// nil profile 表示使用 mode 的内置 profile。仅生产 transport 路径生效。
	TLSProfileResolver TLSProfileResolver
	// Timeouts 仅作用于非流式 buffered dispatch。
	Timeouts TimeoutConfig
	// AnthropicAutoBreakpoints 仅为无客户端 cache_control 的 Messages 请求自动注入断点；
	// 默认关闭，规划失败时保留原始 body。
	AnthropicAutoBreakpoints bool
	// AnthropicTTLSettings 只决定自动断点采用默认 5m 还是显式 1h；读取失败按 5m 处理。
	AnthropicTTLSettings AnthropicTTLSettings
}

// Dispatch 执行一次完整出站。失败时 result 可能为 nil；调用方按 err
// 与 status 决定是否重试 / 回退。
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
	resolvedAccount, _ := ResolveDispatchTransport(in.Account, in.ProtocolFamily)
	in.Account = resolvedAccount
	automaticTransport := in.TransportMode == ""

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

	// RR-04:裁剪超过 cachecontrol.CacheControlMaxAllowed 的 cache_control 断点（上游会拒绝）；失败时保留原请求。
	if trimmed, _ := cachecontrol.EnforceCacheControlLimit(in.InboundBody, cachecontrol.CacheControlMaxAllowed); len(trimmed) > 0 {
		in.InboundBody = trimmed
	}
	// 可选 Anthropic cache_control breakpoint 规划(仅 anthropic_messages、opt-in;见 maybeInjectAnthropicBreakpoints)。
	in.InboundBody = d.maybeInjectAnthropicBreakpoints(ctx, in.ProtocolFamily, in.InboundBody)
	// B1:官方 OpenAI 兼容族流式出站强制 stream_options.include_usage(见 streamusage 子包)。
	in.InboundBody = streamusage.Inject(in.ProtocolFamily, in.InboundBody)

	// 2. 构造出站请求
	req, err := adapter.BuildRequest(ctx, provider.BuildInput{
		HTTPMethod:         in.HTTPMethod,
		UpstreamModelID:    in.UpstreamModelID,
		InboundBody:        in.InboundBody,
		InboundContentType: in.InboundContentType,
		Credential:         in.Credential,
		Account:            in.Account,
		EndpointPath:       in.EndpointPath,
		EndpointQuery:      in.EndpointQuery,
		InboundBetaTokens:  in.InboundBetaTokens,
		ClientStreamIntent: in.ClientStreamIntent,
	})
	if err != nil {
		return nil, fmt.Errorf("dispatcher: BuildRequest 失败: %w", err)
	}
	customEndpoint := provider.RequestUsesCustomPassthroughEndpoint(in.Credential, req.URL)
	if err := validatePassthroughEndpointTarget(ctx, customEndpoint, req); err != nil {
		return nil, err
	}
	if key := strings.TrimSpace(in.IdempotencyKey); key != "" {
		if err := validateOutboundIdempotencyKey(key); err != nil {
			return nil, err
		}
		req.Header.Set("Idempotency-Key", key)
	}
	headerfirewall.StripHopByHopRequestHeaders(req.Header)
	headerfirewall.NormalizeEgressRequestHeaders(req.Header)
	if automaticTransport {
		if customEndpoint {
			// 凭据自配地址不继承厂商官方客户端的 TLS 仿真；标准 transport
			// 才能把预检解析与实际拨号都绑定到统一 SSRF 策略。
			in.TransportMode = transport.TransportModeStandard
		} else {
			in.TransportMode = dispatchTransportModeForRequest(
				transport.ProviderCode(in.Account.Platform),
				in.Account.AccountType,
				req.URL.Path,
			)
		}
	}

	// 3. 取 transport
	mode := in.TransportMode
	rt, err := d.TransportFactory.For(transport.ProviderCode(in.Account.Platform), mode)
	if err != nil {
		return nil, fmt.Errorf("dispatcher: 取 RoundTripper 失败: %w", err)
	}

	// 4. 发出请求
	client := d.HTTPClient
	if client == nil {
		rt, err = d.applyTLSProfile(ctx, rt, mode, in.Account.AccountID)
		if err != nil {
			return nil, err
		}
		rt, err = d.applyProxy(ctx, rt, in.Account.AccountID)
		if err != nil {
			return nil, err
		}
		if customEndpoint {
			rt, err = provider.WrapPassthroughEndpointTransport(rt)
			if err != nil {
				return nil, fmt.Errorf("dispatcher: passthrough endpoint rejected: %w", err)
			}
		}
		client = d.httpClientForRoundTripper(rt, in.NonStreamingBuffered)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, newDispatchOutcomeUnknownError(err)
	}

	// 5. 组装结果
	return &DispatchResult{
		UpstreamReader: resp.Body,
		StatusCode:     resp.StatusCode,
		Headers:        resp.Header,
		Close:          resp.Body.Close,
	}, nil
}

func validateOutboundIdempotencyKey(key string) error {
	if len(key) > 256 {
		return errors.New("dispatcher: Idempotency-Key 超过 256 字节")
	}
	for index := 0; index < len(key); index++ {
		if key[index] < 0x21 || key[index] > 0x7e {
			return errors.New("dispatcher: Idempotency-Key 含非法字符")
		}
	}
	return nil
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

// TLSProfileResolver 按账号解析数据库动态 TLS profile；nil 表示保持内置 profile。
type TLSProfileResolver interface {
	ResolveProfile(ctx context.Context, accountID int64) (*mimicry.InlineTLSProfile, error)
}

// applyTLSProfile 把 resolver 选出的动态 profile 绑定到当前 Rust sidecar transport。
// 没有绑定时保留内置 profile；显式绑定的坏数据、数据库故障或非 sidecar transport
// 都明确失败，避免静默换用另一套 ClientHello。
func (d *UpstreamDispatcher) applyTLSProfile(ctx context.Context, rt http.RoundTripper, mode transport.TransportMode, accountID int64) (http.RoundTripper, error) {
	if d.TLSProfileResolver == nil || accountID == 0 ||
		mode == transport.TransportModeStandard || mode == transport.TransportModeStandardH1 {
		return rt, nil
	}
	profile, err := d.TLSProfileResolver.ResolveProfile(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("dispatcher: 解析账号 TLS profile 失败: %w", err)
	}
	if profile == nil {
		return rt, nil
	}
	binder, ok := rt.(interface {
		WithInlineTLSProfile(*mimicry.InlineTLSProfile) (http.RoundTripper, error)
	})
	if !ok {
		return nil, fmt.Errorf("dispatcher: 动态 TLS profile %q 需要 Rust sidecar transport，实际 %T", profile.ID, rt)
	}
	bound, err := binder.WithInlineTLSProfile(profile)
	if err != nil {
		return nil, fmt.Errorf("dispatcher: 绑定动态 TLS profile %q 失败: %w", profile.ID, err)
	}
	return bound, nil
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
	if proxyURL != nil {
		if err := provider.ValidateProxyEndpointTarget(ctx, proxyURL); err != nil {
			return nil, fmt.Errorf("dispatcher: 代理目标被安全策略拒绝: %w", err)
		}
	}
	return provider.WrapTransportWithProxy(rt, proxyURL), nil
}

func validatePassthroughEndpointTarget(ctx context.Context, customEndpoint bool, req *http.Request) error {
	if !customEndpoint {
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

// maybeInjectAnthropicBreakpoints 不改客户端自带控制字段；任何规划、设置或序列化失败
// 都回退原始 body，缓存优化不得阻断实时请求。
func (d *UpstreamDispatcher) maybeInjectAnthropicBreakpoints(ctx context.Context, protocolFamily string, body []byte) []byte {
	if d == nil || !d.AnthropicAutoBreakpoints || protocolFamily != "anthropic_messages" || len(body) == 0 {
		return body
	}
	// 客户端已自带 cache_control:body 原样保留。
	if cacheplan.HasAnyCacheControl(body) {
		return body
	}
	snapshot, err := cachecontrol.InspectCacheControl(body)
	if err != nil {
		return body
	}
	suggestion, err := cachecontrol.SuggestBreakpoints(body, snapshot, nil)
	if err != nil || len(suggestion.Add) == 0 {
		return body
	}
	apply := cachecontrol.ApplyBreakpoints
	if d.AnthropicTTLSettings != nil {
		if enabled, readErr := d.AnthropicTTLSettings.AnthropicTTL1hRewriteEnabled(ctx); readErr == nil && enabled {
			for i := range suggestion.Add {
				suggestion.Add[i].TTL = "1h"
			}
			apply = cachecontrol.ApplyBreakpointsWithTTLOrdering
		}
	}
	result, err := apply(body, suggestion)
	if err != nil || len(result.Applied) == 0 || len(result.Body) == 0 {
		return body
	}
	return result.Body
}
