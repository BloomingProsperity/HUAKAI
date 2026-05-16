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
	"net/http"
)

// CredentialType 是凭据形态的封闭枚举。
type CredentialType string

const (
	// CredentialTypeAPIKey 普通开发者 API key（如 OpenAI sk-...，Bearer 头注入）
	CredentialTypeAPIKey CredentialType = "apikey"
	// CredentialTypeOAuthAccessToken OAuth 拿到的 access_token（如 Anthropic
	// Pro/Max OAuth、Gemini OAuth、Antigravity OAuth）
	CredentialTypeOAuthAccessToken CredentialType = "oauth_access_token"
	// CredentialTypeSessionToken 网页/客户端 session token 反转用（如
	// ChatGPT Plus / Cursor / Windsurf）
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
	// Platform vendor 平台标识（如 "openai" / "gemini" / "antigravity"
	// / "cursor" / "copilot" / "kiro" / "windsurf"）。
	Platform string
	// AccountType 账号类型（如 "apikey" / "oauth" / "session" / "bedrock"）。
	AccountType string
	// AccountCredentialID 是当前出站凭据行主键，用于 channel-health subject。
	AccountCredentialID int64
	// CredentialVersion 是当前出站凭据版本，用于区分轮换前后的健康状态。
	CredentialVersion int
}

// BuildInput 是 BuildRequest 的入参。
type BuildInput struct {
	// UpstreamModelID 上游真实 model id（registry 已解析；如 binding 重写
	// 后的 "gpt-4o-2024-08-06"）。Adapter 把它写入出站 body 或 query。
	UpstreamModelID string
	// InboundBody 客户原始请求 body 字节（HUAKAI 协议入口已统一形态，
	// 但每家 vendor 适配器决定是否 reshape）。
	InboundBody []byte
	// Credential 凭据。
	Credential Credential
	// Account 池中选中的 account。
	Account AccountInfo
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
