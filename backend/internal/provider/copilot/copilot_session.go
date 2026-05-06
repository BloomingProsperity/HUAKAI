// 包 copilot — GitHub Copilot 网页 session 反转适配器。
//
// CopilotSessionAdapter 目标 endpoint 为 GitHub Copilot 官方聊天补全接口。
// 凭据形态：session_token（GitHub OAuth 登录后颁发的 Copilot token，
// 通常由 github.com/login/oauth 或 VS Code 扩展本地缓存获取）
// 或 upstream_passthrough（caller 已持有完整 Authorization 值）。
//
// Copilot 后端对 Editor-Version / Editor-Plugin-Version 等元信息较为敏感，
// 缺失时可能触发限流或 401；本 adapter 提供合理默认值，caller 可通过 Extra 覆盖。
package copilot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// defaultCopilotEndpoint GitHub Copilot 聊天补全接口。
const defaultCopilotEndpoint = "https://api.githubcopilot.com/chat/completions"

// defaultCopilotUserAgent 模拟 VS Code + Copilot 插件的默认 UA。
// caller 可通过 Extra["user_agent"] 覆盖。
const defaultCopilotUserAgent = "GithubCopilot/1.185.0 vscode/1.89.0"

// 编译期接口合规断言。
var _ provider.Adapter = (*CopilotSessionAdapter)(nil)

// CopilotSessionAdapter 将 GitHub Copilot 登录态转换为 HUAKAI 可路由的出站请求。
//
// 与 PassthroughAdapter 的区别：
//   - 目标是 api.githubcopilot.com（非 api.openai.com）
//   - 需注入编辑器元信息 header（Editor-Version / Editor-Plugin-Version）
//   - OpenAI-Intent 声明请求用途，影响 Copilot 后端行为分支
//   - X-Github-Api-Version 声明 API 兼容版本
type CopilotSessionAdapter struct {
	// Endpoint 允许覆盖默认目标 URL；空值时使用 defaultCopilotEndpoint。
	Endpoint string
}

// Platform 返回平台代码，与 transport.ProviderCode 对齐。
func (a *CopilotSessionAdapter) Platform() string {
	return "copilot"
}

// AcceptableCredentialTypes 声明本 adapter 接受的凭据形态。
func (a *CopilotSessionAdapter) AcceptableCredentialTypes() []provider.CredentialType {
	return []provider.CredentialType{
		provider.CredentialTypeSessionToken,
		provider.CredentialTypeUpstreamPassthrough,
	}
}

// BuildRequest 组装指向 GitHub Copilot 后端的出站 HTTP 请求。
//
// 校验顺序：
//  1. apikey 凭据明确拒绝（Copilot 不使用 OpenAI 格式 apikey）
//  2. 凭据形态白名单检查
//  3. Credential.Value 去空白后非空
//  4. UpstreamModelID 去空白后非空
//  5. 选取目标 endpoint
//  6. 构造带 Context 的 POST 请求
//  7. 注入 Authorization header
//  8. 注入通用 header（Content-Type / Accept / User-Agent）
//  9. 注入 Copilot 特有 header
func (a *CopilotSessionAdapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	// 步骤 1：apikey 明确拒绝
	if in.Credential.Type == provider.CredentialTypeAPIKey {
		return nil, errors.New("copilot session: apikey 凭据不适用——Copilot 使用 OAuth token 而非 OpenAI apikey")
	}
	// 步骤 2：白名单检查
	if !a.acceptsCredential(in.Credential.Type) {
		return nil, fmt.Errorf("copilot session: 不支持的凭据形态 %q", in.Credential.Type)
	}

	// 步骤 3：凭据值非空
	if strings.TrimSpace(in.Credential.Value) == "" {
		return nil, errors.New("copilot session: 凭据值为空——需要有效的 GitHub OAuth / Copilot token")
	}

	// 步骤 4：模型标识非空
	if strings.TrimSpace(in.UpstreamModelID) == "" {
		return nil, errors.New("copilot session: UpstreamModelID 不得为空（如 gpt-4o / claude-3.5-sonnet）")
	}

	// 步骤 5：确定 endpoint
	target := a.Endpoint
	if target == "" {
		target = defaultCopilotEndpoint
	}

	// 步骤 6：构造请求
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(in.InboundBody))
	if err != nil {
		return nil, fmt.Errorf("copilot session: 构造出站请求失败: %w", err)
	}

	// 步骤 7：Authorization
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
		ua = defaultCopilotUserAgent
	}
	req.Header.Set("User-Agent", ua)

	// 步骤 9：Copilot 特有 header
	// Editor-Version：声明编辑器版本，Copilot 后端据此路由不同功能集
	editorVer := in.Credential.Extra["editor_version"]
	if editorVer == "" {
		editorVer = "vscode/1.89.0"
	}
	req.Header.Set("Editor-Version", editorVer)

	// Editor-Plugin-Version：Copilot 插件版本，影响功能标记判断
	pluginVer := in.Credential.Extra["editor_plugin_version"]
	if pluginVer == "" {
		pluginVer = "copilot-chat/0.16.1"
	}
	req.Header.Set("Editor-Plugin-Version", pluginVer)

	// OpenAI-Intent：声明本次请求的业务用途（conversation-panel / inline-chat 等）
	intent := in.Credential.Extra["openai_intent"]
	if intent == "" {
		intent = "conversation-panel"
	}
	req.Header.Set("OpenAI-Intent", intent)

	// X-Github-Api-Version：Copilot API 版本锁定，格式 YYYY-MM-DD
	apiVer := in.Credential.Extra["github_api_version"]
	if apiVer == "" {
		apiVer = "2023-07-07"
	}
	req.Header.Set("X-Github-Api-Version", apiVer)

	// TODO(OCAW): 以下 header 需采集真实流量后补全
	// - x-request-id：UUID v4 格式请求追踪 ID
	// - x-vscode-machineidentifier：机器唯一标识，影响速率限制桶
	// - x-copilot-integration-id：集成来源标识（如 "vscode-chat"）
	// - Copilot-Integration-Id：部分版本使用此大小写变体

	// cookie 透传（含 _octo / logged_in 等 GitHub 登录态 cookie）
	if ck := in.Credential.Extra["cookie"]; ck != "" {
		req.Header.Set("Cookie", ck)
	}

	return req, nil
}

// acceptsCredential 检查凭据形态是否在白名单内。
func (a *CopilotSessionAdapter) acceptsCredential(t provider.CredentialType) bool {
	for _, allowed := range a.AcceptableCredentialTypes() {
		if allowed == t {
			return true
		}
	}
	return false
}

// Source files read: /c/HUAKAI/repo/backend/internal/provider/openai/codex_session.go; Lane: claude; Time: 2026-05-06T00:00:00Z
