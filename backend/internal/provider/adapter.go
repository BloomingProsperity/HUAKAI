// 包 provider 提供 vendor 出站请求适配器抽象。每家 vendor（OpenAI / Gemini /
// Antigravity / Bedrock / Cursor / Copilot / Kiro / Windsurf 等）实现一个
// Adapter，负责把"HUAKAI 内部 ResolvedModel + 客户 inbound body + 池中选中
// 的 account credential"组装成一个可发出去的 *http.Request。
//
// 不在本接口内做的事：
//   - HTTP 拨号 / 连接池（由 transport.Factory 提供 RoundTripper，gateway
//     forwarder 调用 Do() 后把响应体喂给 StreamForwarder）
//   - SSE 解析 / token 计费（由 forwarder + proto adapter 负责）
//   - retry / failover / cooldown（由 router + pool 负责）
//
// Adapter 的职责仅限"知道这家 vendor 的 endpoint、auth header 形态、
// body shape 转换"。意图是让 chat_completions_handler 不再 hard-code
// 任何 vendor 形态，所有差异收敛到对应 Adapter。
package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// CredentialType 是凭据形态的封闭枚举。
type CredentialType string

const (
	// CredentialTypeAPIKey 普通开发者 API key（如 OpenAI sk-...，Bearer 头注入）
	CredentialTypeAPIKey CredentialType = "apikey"
	// CredentialTypeOAuthAccessToken OAuth 拿到的 access_token（如 Anthropic
	// Pro/Max OAuth、Gemini OAuth、Antigravity OAuth 凭据）
	CredentialTypeOAuthAccessToken CredentialType = "oauth_access_token"
	// CredentialTypeSessionToken 网页/客户端 session token 反转用（如
	// ChatGPT Plus / Cursor / Windsurf 会话）
	CredentialTypeSessionToken CredentialType = "session_token"
	// CredentialTypeAWSSigV4 AWS SigV4 已签名凭据（Bedrock 直通）
	CredentialTypeAWSSigV4 CredentialType = "aws_sigv4"
	// CredentialTypeUpstreamPassthrough 上游 base URL + key 透传（自带前缀）
	CredentialTypeUpstreamPassthrough CredentialType = "upstream_passthrough"
)

// Credential 描述一次出站请求要用的凭据。
type Credential struct {
	// Type 凭据形态。
	Type CredentialType
	// Value 主要凭据字符串（API key / access_token / session token）。AWS
	// SigV4 模式下此字段为 Authorization header 完整值。
	Value string
	// Extra 携带 vendor-specific 附加字段（如 OpenAI 的 org_id / project_id；
	// Gemini 的 X-Goog-User-Project；Cursor 的 client_key 等）。
	Extra map[string]string
}

// AccountInfo 是池中选中的 account 摘要，供 adapter 在构造请求时引用
// （如 binding-level 模型映射、组级配额标记等）。仅含 adapter 必需字段。
type AccountInfo struct {
	// AccountID 池中 account 主键。
	AccountID int64
	// TenantID 是账号所属租户; CredentialVault.Resolve 与 dispatcher 后续都
	// 用它做 DR-001 跨租户隔离校验。
	TenantID int64
	// Platform vendor 平台标识（如 "openai" / "gemini" / "antigravity"
	// / "cursor" / "copilot" / "kiro" / "windsurf"）。
	Platform string
	// AccountType 账号类型（如 "apikey" / "oauth" / "session" / "bedrock"）。
	AccountType string
	// CodexCLIOnly 表示该账号 opt-in 到 Codex CLI 入站官方客户端门。该字段仅供
	// 入站门控使用，禁止投影到出站 Credential.Extra 或身份改写载荷；缺省 false
	// 表示维持 Codex/OpenAI 账号默认放开。
	CodexCLIOnly bool
	// AccountCredentialID 是当前出站凭据行主键，用于 channel-health subject。
	AccountCredentialID int64
	// CredentialVersion 是当前出站凭据版本，用于区分轮换前后的健康状态。
	CredentialVersion int
	// ExternalAccountID 是上游 provider 账号的稳定标识（如 Anthropic account
	// uuid），凭据获取时自动提取、与凭据行 1:1 同生命周期存于
	// account_credentials.external_account_id（迁移 0141）。账号管理元数据，
	// **非鉴权/计费/配额输入**；仅供 R7 身份改写（mimicryidentity）把它投影进
	// metadata.user_id 的 account 组件。未提取到时为空串 → 下游 fail-open 不改写。
	ExternalAccountID string
}

// EndpointForCredential 按账号凭据和 operator 配置决定上游 endpoint:
//   - 默认走 adapter.Endpoint (或调用方传的 adapterDefault)
//   - APIKey 或 UpstreamPassthrough 凭据的 Extra["base_url"] 非空时，改用
//     operator 自配的上游地址
//
// base_url 路径处理:
//   - base_url 只含 scheme + host (e.g. "https://proxy.com" 或 "https://proxy.com/") →
//     用 adapter default 的 path 拼接, 结果 "https://proxy.com/v1/chat/completions"
//   - base_url 自带 path (e.g. "https://proxy.com/api/v1/chat") → 信任用户原样返回
//
// 自定义 base_url 必先通过 safePassthroughBaseURL 静态校验；默认策略对 metadata、
// 内网、loopback 与其它特殊用途目标 fail-closed。dispatcher 仍须在发网前执行
// DNS 校验并在直连拨号时再次约束目标 IP，避免域名解析与重绑定绕过。
// APIKey 与 UpstreamPassthrough 未配置 base_url 时都回落 adapterDefault。
func EndpointForCredential(adapterDefault string, cred Credential) (string, error) {
	if cred.Type != CredentialTypeAPIKey && cred.Type != CredentialTypeUpstreamPassthrough {
		return adapterDefault, nil
	}
	base := strings.TrimSpace(cred.Extra["base_url"])
	if base == "" {
		return adapterDefault, nil
	}
	baseURL, err := safePassthroughBaseURL(base)
	if err != nil {
		return "", err
	}
	defaultURL, err := url.Parse(adapterDefault)
	if err != nil || defaultURL.Host == "" {
		return "", fmt.Errorf("%w: invalid adapter default endpoint", ErrUnsafePassthroughEndpoint)
	}
	defaultPath := defaultURL.Path
	basePath := strings.TrimRight(baseURL.Path, "/")
	// adapterSuffix = adapter default path 中 API 版本段之后的 endpoint 部分,
	// "/v1/chat/completions" 与 "/api/v1/chat/completions" 都得 "/chat/completions"。
	// 之前只剥首段, 但 OpenRouter "/api/v1/chat/completions" 与 Groq
	// "/openai/v1/chat/completions" 的版本段是第二段, 只剥首段会留下
	// "/v1/chat/completions", 拼到 base "/api/v1" 即 "/api/v1/v1/chat/completions"。
	adapterSuffix := apiEndpointSuffix(defaultPath)
	combined := *baseURL
	switch {
	case basePath == "" || basePath == "/":
		// base 仅 scheme+host → 用 adapter 的全 path
		combined.Path = defaultPath
	case strings.HasSuffix(basePath, defaultPath) || strings.HasSuffix(basePath, adapterSuffix):
		// base 已含完整 adapter path / endpoint suffix → 信任原值
		combined.Path = basePath
	case isAPIVersionPath(basePath):
		// base path 末段是 API 版本号 (e.g. /v1 或 /api/v1) → 只拼 endpoint
		// suffix, 不重复版本段, 避免 /api/v1/v1/chat/completions。
		combined.Path = basePath + adapterSuffix
	default:
		// base path 末段不是版本号 (e.g. /api/v1/chat 自定义完整路径) → 信任
		// 用户配置, 原样用, 不再硬拼后缀。
		combined.Path = basePath
	}
	// base_url 自带 query (proxy routing token / Azure
	// api-version 等) 必须保留; 仅 base 无 query 时才用 adapter default query。
	if combined.RawQuery == "" {
		combined.RawQuery = defaultURL.RawQuery
	}
	return combined.String(), nil
}

// EndpointForBuildInput 在现有的凭据相关 endpoint 选择之前,先套用一个可选的
// endpoint path 覆盖。EndpointPath 为空时保持 adapter 的既有行为。
func EndpointForBuildInput(adapterDefault string, in BuildInput) (string, error) {
	defaultEndpoint := strings.TrimSpace(adapterDefault)
	if path := strings.TrimSpace(in.EndpointPath); path != "" {
		u, err := url.Parse(defaultEndpoint)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return "", fmt.Errorf("%w: invalid adapter default endpoint", ErrUnsafePassthroughEndpoint)
		}
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		u.Path = path
		u.RawPath = ""
		defaultEndpoint = u.String()
	}
	return EndpointForCredential(defaultEndpoint, in.Credential)
}

func EndpointWithQueryParamIfMissing(endpoint, key, value string) (string, error) {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return endpoint, nil
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	q := u.Query()
	if q.Get(key) != "" {
		return endpoint, nil
	}
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// isAPIVersionSegment 判断单个 path 段是否是 API 版本号: 以 'v' 开头, 紧跟
// 至少一个数字 (允许 v1 / v2 / v1beta / v1alpha 等)。
func isAPIVersionSegment(seg string) bool {
	if len(seg) < 2 || seg[0] != 'v' {
		return false
	}
	return seg[1] >= '0' && seg[1] <= '9'
}

// isAPIVersionPath 判断 path 末段是否是 API 版本号。 末段是版本号 →
// base_url 是 API root 应拼 endpoint suffix; 否则视为用户自定义完整路径,
// 原样信任。
func isAPIVersionPath(path string) bool {
	idx := strings.LastIndex(path, "/")
	last := path
	if idx >= 0 {
		last = path[idx+1:]
	}
	return isAPIVersionSegment(last)
}

// apiEndpointSuffix 返回 adapter default path 中 API 版本段之后的 endpoint
// 部分。"/v1/chat/completions" 与 "/api/v1/chat/completions" 都返回
// "/chat/completions"。 取**首个**版本段而非最后一个 — 否则 gemini
// "/v1beta/models/{model}:..." 里若 model 名形如 "v2-pro" 会被误判成版本段
// 找不到版本段, 或版本段已是末段时, 原样返回整个 path。
func apiEndpointSuffix(path string) string {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for i, seg := range segs {
		if !isAPIVersionSegment(seg) {
			continue
		}
		if i >= len(segs)-1 {
			return path
		}
		return "/" + strings.Join(segs[i+1:], "/")
	}
	return path
}

// BuildInput 是 BuildRequest 的入参。
type BuildInput struct {
	// UpstreamModelID 上游真实 model id（registry 已解析；如 binding 重写
	// 后的 "gpt-4o-2024-08-06"）。Adapter 把它写入出站 body 或 query。
	UpstreamModelID string
	// InboundBody 客户原始请求 body 字节（HUAKAI 协议入口已统一形态，
	// 但每家 vendor 适配器决定是否 reshape）。
	InboundBody []byte
	// InboundContentType 在原始 body 必须逐字节透传时(如 multipart audio)
	// 携带调用方解析出的 Content-Type。为空时保持既有的 JSON 透传行为。
	InboundContentType string
	// EndpointPath 可选覆盖 adapter 默认 endpoint path。空值保持 adapter
	// 默认；OpenAI-compatible embeddings passthrough 使用 "/v1/embeddings"。
	EndpointPath string
	// Credential 凭据。
	Credential Credential
	// Account 池中选中的 account。
	Account AccountInfo
	// InboundBetaTokens 客户端 anthropic-beta 请求头解析出的 beta token
	// 列表(ParseInboundBetaTokens 产出:已 trim/小写/语法校验/去重/限长)。
	// 仅 anthropic 族 adapter 消费——与凭据 Extra["anthropic_beta"] 合并
	// 去重后写出站 Anthropic-Beta;其余 adapter 忽略本字段。
	InboundBetaTokens []string
	// ClientStreamIntent 表示客户端请求了流式响应(来自入口解析后的 resolved
	// stream intent)。仅作 OR 信号:true=确定要流式;false=未知/非流,不构成
	// 非流断言。gemini-shaped 族(gemini_messages/vertex_gemini/
	// gemini_code_assist)的流式由出站 URL action 表达,跨协议 ingress 时
	// marshal 出的 gemini body 无顶层 stream 字段、Extra["stream"] 仅 gemini
	// ingress 注入——没有本字段,openai/anthropic 客户端打 gemini-shaped 上游
	// 的流式请求会被错选到非流 :generateContent。消费优先级(各 adapter 内):
	// Extra["stream"] 显式值 > 本字段 > body 探测。
	ClientStreamIntent bool
}

// Adapter 是 vendor-specific 出站适配器接口。
type Adapter interface {
	// BuildRequest 按 input 构造一个 *http.Request。返回的 request 可
	// 被任意 http.Client / transport.RoundTripper 调用 Do()。
	//
	// Adapter 不做拨号、不做实际网络 IO；任何 IO 失败由调用方处理。
	// Adapter 内部不可发起子请求或读取外部资源。
	BuildRequest(ctx context.Context, in BuildInput) (*http.Request, error)

	// Platform 返回 adapter 服务的平台标识。供 admin trace + audit 渲染。
	Platform() string

	// AcceptableCredentialTypes 返回 adapter 支持的凭据形态集合。
	// AccountInfo.AccountType 不在此集合时，BuildRequest 应返回 error。
	AcceptableCredentialTypes() []CredentialType
}
