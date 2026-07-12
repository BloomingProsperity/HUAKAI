// 包 kiro — AWS Kiro AI 编辑器 session 反转适配器。
//
// KiroSessionAdapter 目标 endpoint 为 AWS Kiro（2025 年发布的 AI 编辑器）
// 后端推理接口。Kiro 基于 AWS 基础设施，凭据体系推测依赖 Cognito 颁发的
// ID Token 或 Access Token；具体格式待 OCAW 层采集真实流量后确认。
//
// 凭据形态：session_token（Cognito / AWS SSO 令牌）
// 或 upstream_passthrough（caller 已持有完整 Authorization 值）。
//
// 注意：Kiro 为 2025 年新发布产品，endpoint 路径及 header 结构处于
// 快速迭代阶段，所有 vendor 特定字段均为推测性占位，OCAW 采集前请勿上线。
package kiro

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// defaultKiroEndpoint Kiro 后端推理接口占位 URL。
// TODO(OCAW): 采集真实 Kiro 客户端流量后替换；推测为 AWS API Gateway 路径
const defaultKiroEndpoint = "https://api.kiro.aws/v1/chat/completions" // TODO(OCAW): 待确认实际域名与路径

// defaultKiroUserAgent 模拟 Kiro 桌面客户端风格 UA（占位）。
// TODO(OCAW): 确认 Kiro 客户端实际 UA 格式
const defaultKiroUserAgent = "Kiro/1.0.0 (linux; x64; aws)"

// 编译期接口合规断言。
var _ provider.Adapter = (*KiroSessionAdapter)(nil)

// KiroSessionAdapter 将 AWS Kiro 登录态转换为 HUAKAI 可路由的出站请求。
//
// 推测特征（待 OCAW 验证）：
//   - 凭据为 AWS Cognito ID Token（JWT 格式），Bearer 注入
//   - 后端基于 AWS API Gateway + Lambda，需 x-amzn-* 系列追踪 header
//   - 可能需要 x-aws-cognito-id-token 作为二级鉴权字段
//   - body 格式推测兼容 OpenAI chat/completions 结构
type KiroSessionAdapter struct {
	// Endpoint 允许覆盖默认目标 URL；空值时使用 defaultKiroEndpoint。
	Endpoint string
}

// Platform 返回平台代码，与 transport.ProviderCode 对齐。
func (a *KiroSessionAdapter) Platform() string {
	return "kiro"
}

// AcceptableCredentialTypes 声明本 adapter 接受的凭据形态。
func (a *KiroSessionAdapter) AcceptableCredentialTypes() []provider.CredentialType {
	return []provider.CredentialType{
		provider.CredentialTypeSessionToken,
		provider.CredentialTypeUpstreamPassthrough,
	}
}

// BuildRequest 组装指向 Kiro 后端的出站 HTTP 请求。
//
// 校验顺序：
//  1. apikey 凭据明确拒绝
//  2. 凭据形态白名单检查
//  3. Credential.Value 去空白后非空（应为 Cognito JWT）
//  4. UpstreamModelID 去空白后非空
//  5. 选取目标 endpoint
//  6. 构造带 Context 的 POST 请求
//  7. 注入 Authorization header
//  8. 注入通用 header
//  9. 注入 Kiro / AWS 特有 header（含 TODO 占位）
func (a *KiroSessionAdapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	// 步骤 1：apikey 明确拒绝
	if in.Credential.Type == provider.CredentialTypeAPIKey {
		return nil, errors.New("kiro session: apikey 凭据不适用——Kiro 使用 AWS Cognito 令牌而非标准 apikey")
	}
	// 步骤 2：白名单检查
	if !a.acceptsCredential(in.Credential.Type) {
		return nil, fmt.Errorf("kiro session: 凭据形态 %q 不受支持", in.Credential.Type)
	}

	// 步骤 3：凭据值非空（应为 Cognito ID Token）
	if strings.TrimSpace(in.Credential.Value) == "" {
		return nil, errors.New("kiro session: 凭据值为空——需提供有效的 AWS Cognito 登录令牌")
	}

	// 步骤 4：模型标识非空
	if strings.TrimSpace(in.UpstreamModelID) == "" {
		return nil, errors.New("kiro session: UpstreamModelID 不得为空（如 claude-3-7-sonnet-20250219）")
	}

	// 步骤 5：确定 endpoint
	target := a.Endpoint
	if target == "" {
		target = defaultKiroEndpoint
	}

	// 步骤 6：构造请求
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(in.InboundBody))
	if err != nil {
		return nil, fmt.Errorf("kiro session: 构造出站请求失败: %w", err)
	}

	// 步骤 7：Authorization header
	switch in.Credential.Type {
	case provider.CredentialTypeSessionToken:
		// Cognito ID Token 以 Bearer 形态注入
		req.Header.Set("Authorization", "Bearer "+in.Credential.Value)
	case provider.CredentialTypeUpstreamPassthrough:
		req.Header.Set("Authorization", in.Credential.Value)
	}

	// 步骤 8：通用 header
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	ua := in.Credential.Extra["user_agent"]
	if ua == "" {
		ua = defaultKiroUserAgent
	}
	req.Header.Set("User-Agent", ua)

	// 步骤 9：Kiro / AWS 特有 header
	// x-aws-cognito-id-token：部分 AWS 服务要求将 Cognito ID Token 单独传入此字段
	// TODO(OCAW): 确认 Kiro 是否使用此字段及其确切名称
	if cognitoToken := in.Credential.Extra["cognito_id_token"]; cognitoToken != "" {
		req.Header.Set("x-aws-cognito-id-token", cognitoToken)
	}

	// x-amzn-requestid：AWS 追踪请求 ID，UUID v4 格式
	// TODO(OCAW): 确认是否需要客户端生成或由 API Gateway 自动注入
	if reqID := in.Credential.Extra["amzn_request_id"]; reqID != "" {
		req.Header.Set("x-amzn-requestid", reqID)
	}

	// TODO(OCAW): 以下 header 需采集真实 Kiro 客户端流量后确认
	// - x-amzn-trace-id：AWS X-Ray 追踪链路 ID（Root=1-xxx 格式）
	// - x-kiro-client-version：Kiro 编辑器版本号
	// - x-kiro-workspace-id：工作区唯一标识
	// - x-kiro-session-id：编辑器会话 ID
	// - x-amz-security-token：STS 临时凭据场景下的 session token
	// - 是否需要 AWS Signature V4 签名（API Gateway IAM 鉴权模式）

	return req, nil
}

// acceptsCredential 检查凭据形态是否在白名单内。
func (a *KiroSessionAdapter) acceptsCredential(t provider.CredentialType) bool {
	for _, allowed := range a.AcceptableCredentialTypes() {
		if allowed == t {
			return true
		}
	}
	return false
}

// 参考源文件: /c/HUAKAI/repo/backend/internal/provider/openai/codex_session.go; 泳道: claude; 时间: 2026-05-06T00:00:00Z
