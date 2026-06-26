// 包 antigravity — Antigravity AI 网页 session 反转适配器。
//
// AntigravitySessionAdapter 目标 endpoint 待 OCAW 层采集真实流量后确认。
// 凭据形态：session_token（Antigravity 登录后颁发的令牌，具体格式待确认）
// 或 upstream_passthrough（caller 已持有完整 Authorization 值）。
//
// 注意：Antigravity 为新兴 AI 服务，endpoint 路径、header 结构、body 格式
// 均为 TODO 占位状态，所有字段在 OCAW 采集真实流量前不保证有效。
// 本文件仅提供 adapter 框架，确保编译通过并与 provider.Adapter 接口对齐。
package antigravity

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// defaultAntigravityEndpoint Antigravity 聊天接口占位 URL。
// TODO(OCAW): 采集真实流量后替换为实际 endpoint
const defaultAntigravityEndpoint = "https://api.antigravity.ai/v1/chat/completions" // TODO(OCAW): 待确认

// defaultAntigravityUserAgent 占位 UA，待真实流量采集后对齐客户端实际值。
// TODO(OCAW): 确认 Antigravity 客户端 UA 格式
const defaultAntigravityUserAgent = "antigravity-client/1.0.0" // TODO(OCAW): 待确认

// 编译期接口合规断言。
var _ provider.Adapter = (*AntigravitySessionAdapter)(nil)

// AntigravitySessionAdapter 将 Antigravity 登录态转换为 HUAKAI 可路由的出站请求。
//
// 当前状态：框架就绪，所有 vendor 特定字段待 OCAW 采集补全。
// 优先级：低（vendor 尚未被大规模验证）。
type AntigravitySessionAdapter struct {
	// Endpoint 允许覆盖默认目标 URL；空值时使用 defaultAntigravityEndpoint。
	Endpoint string
}

// Platform 返回平台代码，与 transport.ProviderCode 对齐。
func (a *AntigravitySessionAdapter) Platform() string {
	return "antigravity"
}

// AcceptableCredentialTypes 声明本 adapter 接受的凭据形态。
func (a *AntigravitySessionAdapter) AcceptableCredentialTypes() []provider.CredentialType {
	return []provider.CredentialType{
		provider.CredentialTypeSessionToken,
		provider.CredentialTypeUpstreamPassthrough,
	}
}

// BuildRequest 组装指向 Antigravity 后端的出站 HTTP 请求。
//
// 校验顺序：
//  1. apikey 凭据明确拒绝
//  2. 凭据形态白名单检查
//  3. Credential.Value 去空白后非空
//  4. UpstreamModelID 去空白后非空
//  5. 选取目标 endpoint
//  6. 构造带 Context 的 POST 请求
//  7. 注入 Authorization header
//  8. 注入通用 header
//  9. 注入 vendor 特有 header（全部 TODO 占位）
func (a *AntigravitySessionAdapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	// 步骤 1：apikey 明确拒绝
	if in.Credential.Type == provider.CredentialTypeAPIKey {
		return nil, errors.New("antigravity session: apikey 凭据不适用于本 adapter；Antigravity 使用 session token 鉴权")
	}
	// 步骤 2：白名单检查
	if !a.acceptsCredential(in.Credential.Type) {
		return nil, fmt.Errorf("antigravity session: 凭据形态 %q 不在受支持列表中", in.Credential.Type)
	}

	// 步骤 3：凭据值非空
	if strings.TrimSpace(in.Credential.Value) == "" {
		return nil, errors.New("antigravity session: 凭据值不得为空——需提供有效的 Antigravity 登录令牌")
	}

	// 步骤 4：模型标识非空
	if strings.TrimSpace(in.UpstreamModelID) == "" {
		return nil, errors.New("antigravity session: UpstreamModelID 不得为空")
	}

	// 步骤 5：确定 endpoint
	target := a.Endpoint
	if target == "" {
		target = defaultAntigravityEndpoint
	}

	// 步骤 6：构造请求
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(in.InboundBody))
	if err != nil {
		return nil, fmt.Errorf("antigravity session: 构造出站请求失败: %w", err)
	}

	// 步骤 7：Authorization header
	switch in.Credential.Type {
	case provider.CredentialTypeSessionToken:
		req.Header.Set("Authorization", "Bearer "+in.Credential.Value)
	case provider.CredentialTypeUpstreamPassthrough:
		req.Header.Set("Authorization", in.Credential.Value)
	}

	// 步骤 8：通用 header
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	ua := in.Credential.Extra["user_agent"]
	if ua == "" {
		ua = defaultAntigravityUserAgent
	}
	req.Header.Set("User-Agent", ua)

	// 步骤 9：Antigravity 特有 header（全部待采集）
	// TODO(OCAW): 以下所有字段均为占位，需采集真实 Antigravity 客户端流量后确认
	// - 鉴权相关：token 刷新机制、cookie 结构待确认
	// - 设备标识：客户端 fingerprint 字段名及格式待确认
	// - 速率限制：请求 ID / 追踪 ID 字段名待确认
	// - 版本声明：客户端版本 header 名称及格式待确认
	// - CORS 相关：Origin / Referer 是否强校验待确认

	// cookie 透传（如 Antigravity 使用 cookie 鉴权）
	if ck := in.Credential.Extra["cookie"]; ck != "" {
		req.Header.Set("Cookie", ck)
	}

	return req, nil
}

// acceptsCredential 检查凭据形态是否在白名单内。
func (a *AntigravitySessionAdapter) acceptsCredential(t provider.CredentialType) bool {
	for _, allowed := range a.AcceptableCredentialTypes() {
		if allowed == t {
			return true
		}
	}
	return false
}

// 参考读取的源文件:/c/HUAKAI/repo/backend/internal/provider/openai/codex_session.go;泳道:claude;时间:2026-05-06T00:00:00Z
