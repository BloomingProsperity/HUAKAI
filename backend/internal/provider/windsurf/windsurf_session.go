// 包 windsurf — Codeium Windsurf 编辑器 session 反转适配器。
//
// WindsurfSessionAdapter 目标 endpoint 为 Codeium Windsurf 编辑器后端推理接口。
// Windsurf 由 Codeium 团队开发，凭据体系与 Codeium 扩展共享：
// 登录后颁发的 API token 存储于本地配置文件，格式为不透明字符串。
//
// 凭据形态：session_token（Windsurf / Codeium 登录 token）
// 或 upstream_passthrough（caller 已持有完整 Authorization 值）。
//
// 注意：Windsurf 后端 endpoint 未公开文档化，此处为推测性占位；
// 具体路径、body 格式及必要 header 需 OCAW 采集真实流量后确认。
package windsurf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// defaultWindsurfEndpoint Windsurf 后端推理接口占位 URL。
// TODO(OCAW): 采集真实 Windsurf 客户端流量后替换；实际域名可能为 codeium.com 子域
const defaultWindsurfEndpoint = "https://api.codeium.com/exa/windsurf_v2/chat/completions" // TODO(OCAW): 待确认

// defaultWindsurfUserAgent 模拟 Windsurf 桌面客户端风格 UA。
// TODO(OCAW): 确认 Windsurf 客户端实际 UA 格式及版本号
const defaultWindsurfUserAgent = "Windsurf/1.0.0 (linux; x64; codeium)"

// 编译期接口合规断言。
var _ provider.Adapter = (*WindsurfSessionAdapter)(nil)

// WindsurfSessionAdapter 将 Codeium Windsurf 登录态转换为 HUAKAI 可路由的出站请求。
//
// 关键特征：
//   - 凭据为 Codeium / Windsurf 登录 token（不透明字符串，非 JWT）
//   - 需注入 codeium-extension-version 声明客户端版本
//   - X-Codeium-Telemetry-Tags 用于 Codeium 后端行为分析（可能影响功能路由）
//   - body 格式推测兼容 OpenAI chat/completions 结构，含 Windsurf 特有扩展字段
type WindsurfSessionAdapter struct {
	// Endpoint 允许覆盖默认目标 URL；空值时使用 defaultWindsurfEndpoint。
	Endpoint string
}

// Platform 返回平台代码，与 transport.ProviderCode 对齐。
func (a *WindsurfSessionAdapter) Platform() string {
	return "windsurf"
}

// AcceptableCredentialTypes 声明本 adapter 接受的凭据形态。
func (a *WindsurfSessionAdapter) AcceptableCredentialTypes() []provider.CredentialType {
	return []provider.CredentialType{
		provider.CredentialTypeSessionToken,
		provider.CredentialTypeUpstreamPassthrough,
	}
}

// BuildRequest 组装指向 Windsurf 后端的出站 HTTP 请求。
//
// 校验顺序：
//  1. apikey 凭据明确拒绝
//  2. 凭据形态白名单检查
//  3. Credential.Value 去空白后非空（Codeium 登录 token）
//  4. UpstreamModelID 去空白后非空
//  5. 选取目标 endpoint
//  6. 构造带 Context 的 POST 请求
//  7. 注入 Authorization header
//  8. 注入通用 header
//  9. 注入 Windsurf / Codeium 特有 header
func (a *WindsurfSessionAdapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	// 步骤 1：apikey 明确拒绝
	if in.Credential.Type == provider.CredentialTypeAPIKey {
		return nil, errors.New("windsurf session: apikey 凭据不适用——Windsurf 使用 Codeium 登录 token 而非标准 apikey")
	}
	// 步骤 2：白名单检查
	if !a.acceptsCredential(in.Credential.Type) {
		return nil, fmt.Errorf("windsurf session: 凭据形态 %q 不受支持", in.Credential.Type)
	}

	// 步骤 3：凭据值非空（Codeium 登录 token）
	if strings.TrimSpace(in.Credential.Value) == "" {
		return nil, errors.New("windsurf session: 凭据值为空——需提供有效的 Codeium / Windsurf 登录令牌")
	}

	// 步骤 4：模型标识非空
	if strings.TrimSpace(in.UpstreamModelID) == "" {
		return nil, errors.New("windsurf session: UpstreamModelID 不得为空（如 windsurf-claude-3-5-sonnet 等）")
	}

	// 步骤 5：确定 endpoint
	target := a.Endpoint
	if target == "" {
		target = defaultWindsurfEndpoint
	}

	// 步骤 6：构造请求
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(in.InboundBody))
	if err != nil {
		return nil, fmt.Errorf("windsurf session: 构造出站请求失败: %w", err)
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
		ua = defaultWindsurfUserAgent
	}
	req.Header.Set("User-Agent", ua)

	// 步骤 9：Windsurf / Codeium 特有 header
	// codeium-extension-version：声明 Windsurf/Codeium 插件版本，影响功能路由
	extVer := in.Credential.Extra["codeium_extension_version"]
	if extVer == "" {
		extVer = "1.8.40"
	}
	req.Header.Set("codeium-extension-version", extVer)

	// X-Codeium-Telemetry-Tags：遥测标签，Codeium 后端据此进行 A/B 分流
	// TODO(OCAW): 确认该字段格式（推测为 JSON 或逗号分隔的 key=value 串）
	if telemetryTags := in.Credential.Extra["codeium_telemetry_tags"]; telemetryTags != "" {
		req.Header.Set("X-Codeium-Telemetry-Tags", telemetryTags)
	}

	// TODO(OCAW): 以下 header 需采集真实 Windsurf 客户端流量后确认
	// - x-codeium-ide-name：IDE 标识符（如 "windsurf"）
	// - x-codeium-ide-version：Windsurf 编辑器版本号
	// - x-codeium-client-type：客户端类型标识（chat / completion / etc.）
	// - x-request-id：请求追踪 UUID
	// - codeium-common-flags：功能开关位域，格式待确认
	// - Origin / Referer：是否有来源校验待确认

	return req, nil
}

// acceptsCredential 检查凭据形态是否在白名单内。
func (a *WindsurfSessionAdapter) acceptsCredential(t provider.CredentialType) bool {
	for _, allowed := range a.AcceptableCredentialTypes() {
		if allowed == t {
			return true
		}
	}
	return false
}

// 已读源文件: /c/HUAKAI/repo/backend/internal/provider/openai/codex_session.go; Lane: claude; Time: 2026-05-06T00:00:00Z
