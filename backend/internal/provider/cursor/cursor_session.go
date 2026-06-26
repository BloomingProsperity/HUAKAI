// 包 cursor — Cursor 编辑器网页 session 反转适配器。
//
// CursorSessionAdapter 目标 endpoint 为 Cursor 内置 AI 服务 gRPC-gateway。
// 凭据形态：session_token（Cursor 登录后写入 ~/.cursor/auth.json 的令牌）
// 或 upstream_passthrough（caller 已格式化完整 Authorization）。
// 普通 OpenAI apikey 不可用于此 adapter，请走专用 PassthroughAdapter。
//
// Cursor 使用 gRPC-gateway over HTTP/2，Content-Type 为 application/connect+proto，
// 但 HUAKAI 代理层在 caller 组装 body 后透传，adapter 仅负责 Auth + header 注入。
package cursor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// defaultCursorEndpoint Cursor AI 流式聊天接口（gRPC-gateway 路径）。
const defaultCursorEndpoint = "https://api2.cursor.sh/aiserver.v1.AiService/StreamChat"

// defaultCursorUserAgent 模拟 Cursor 桌面客户端风格 UA。
// caller 可通过 Credential.Extra["user_agent"] 覆盖。
const defaultCursorUserAgent = "cursor-editor/0.43.6 (linux; x64)"

// 编译期接口合规断言：CursorSessionAdapter 必须实现 provider.Adapter。
var _ provider.Adapter = (*CursorSessionAdapter)(nil)

// CursorSessionAdapter 把 Cursor 登录 session 反转为可被 HUAKAI 路由层消费的出站请求。
//
// 关键点：
//   - 仅接受 session_token / upstream_passthrough
//   - 目标是 api2.cursor.sh（非 api.openai.com）
//   - 需注入 Cursor 特有校验头（checksum / client-version）
//   - x-amzn-trace-id 等 AWS 追踪头由 OCAW 层注入，此处留 TODO
type CursorSessionAdapter struct {
	// Endpoint 允许覆盖默认目标 URL；空值时使用 defaultCursorEndpoint。
	Endpoint string
}

// Platform 返回平台代码，与 transport.ProviderCode 对齐。
func (a *CursorSessionAdapter) Platform() string {
	return "cursor"
}

// AcceptableCredentialTypes 声明本 adapter 接受的凭据形态。
// apikey 凭据被明确拒绝。
func (a *CursorSessionAdapter) AcceptableCredentialTypes() []provider.CredentialType {
	return []provider.CredentialType{
		provider.CredentialTypeSessionToken,
		provider.CredentialTypeUpstreamPassthrough,
	}
}

// BuildRequest 组装指向 Cursor 后端的出站 HTTP 请求。
//
// 校验顺序：
//  1. apikey 凭据明确拒绝
//  2. 凭据形态必须在 AcceptableCredentialTypes 范围内
//  3. Credential.Value 去空白后不得为空
//  4. UpstreamModelID 去空白后不得为空
//  5. 选取目标 endpoint（实例字段优先）
//  6. 创建带 Context 的 POST 请求
//  7. 注入 Authorization header
//  8. 注入通用 header（Content-Type / Accept / User-Agent）
//  9. 注入 Cursor 特有 header
func (a *CursorSessionAdapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	// 步骤 1：apikey 明确拒绝
	if in.Credential.Type == provider.CredentialTypeAPIKey {
		return nil, errors.New("cursor session: apikey 凭据不适用于本 adapter；请改用针对 api.openai.com 的 PassthroughAdapter")
	}
	// 步骤 2：凭据形态白名单检查
	if !a.acceptsCredential(in.Credential.Type) {
		return nil, fmt.Errorf("cursor session: 凭据形态 %q 不在受支持列表中", in.Credential.Type)
	}

	// 步骤 3：凭据值非空检查
	if strings.TrimSpace(in.Credential.Value) == "" {
		return nil, errors.New("cursor session: 凭据值为空——需提供有效的 Cursor session token")
	}

	// 步骤 4：模型 ID 非空检查
	if strings.TrimSpace(in.UpstreamModelID) == "" {
		return nil, errors.New("cursor session: UpstreamModelID 不能为空（对应 Cursor 模型标识符）")
	}

	// 步骤 5：确定目标 endpoint
	target := a.Endpoint
	if target == "" {
		target = defaultCursorEndpoint
	}

	// 步骤 6：构造 HTTP 请求
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(in.InboundBody))
	if err != nil {
		return nil, fmt.Errorf("cursor session: 构造出站请求失败: %w", err)
	}

	// 步骤 7：Authorization header
	switch in.Credential.Type {
	case provider.CredentialTypeSessionToken:
		req.Header.Set("Authorization", "Bearer "+in.Credential.Value)
	case provider.CredentialTypeUpstreamPassthrough:
		req.Header.Set("Authorization", in.Credential.Value)
	}

	// 步骤 8：通用 header
	req.Header.Set("Content-Type", "application/connect+proto")
	req.Header.Set("Accept", "application/connect+proto")

	ua := in.Credential.Extra["user_agent"]
	if ua == "" {
		ua = defaultCursorUserAgent
	}
	req.Header.Set("User-Agent", ua)

	// 步骤 9：Cursor 特有 header
	// x-cursor-checksum：客户端完整性校验摘要，Cursor 风控必要字段
	if checksum := in.Credential.Extra["cursor_checksum"]; checksum != "" {
		req.Header.Set("x-cursor-checksum", checksum)
	}
	// x-cursor-client-version：匹配桌面客户端版本号
	clientVer := in.Credential.Extra["cursor_client_version"]
	if clientVer == "" {
		clientVer = "0.43.6"
	}
	req.Header.Set("x-cursor-client-version", clientVer)

	// TODO(OCAW): 以下 header 需在真实流量采集后由 OCAW 层补全并注入
	// - x-amzn-trace-id：AWS X-Ray 追踪 ID，格式 Root=1-xxx-yyy
	// - x-cursor-timezone：客户端时区字符串（如 "Asia/Shanghai"）
	// - x-cursor-request-id：请求唯一 ID，UUID v4 格式

	// cookie 透传（含 WorkosCursorSessionToken 等登录态 cookie）
	if ck := in.Credential.Extra["cookie"]; ck != "" {
		req.Header.Set("Cookie", ck)
	}

	return req, nil
}

// acceptsCredential 检查凭据形态是否在白名单内。
func (a *CursorSessionAdapter) acceptsCredential(t provider.CredentialType) bool {
	for _, allowed := range a.AcceptableCredentialTypes() {
		if allowed == t {
			return true
		}
	}
	return false
}

// 参阅的资料来源: /c/HUAKAI/repo/backend/internal/provider/openai/codex_session.go; 通道: claude; 时间: 2026-05-06T00:00:00Z
