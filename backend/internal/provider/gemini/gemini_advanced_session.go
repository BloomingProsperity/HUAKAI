// 包 gemini — Google Gemini Advanced 网页 session 反转适配器。
//
// GeminiAdvancedSessionAdapter 目标 endpoint 为 Google Gemini 网页前端的
// BardChatUi 数据接口（与 Gemini Advanced 订阅绑定，非 AI Studio API）。
// 凭据形态：session_token（Google 登录后的 __Secure-1PSID / __Secure-3PSID cookie
// 组合，或经 OCAW 层封装的等效令牌）或 upstream_passthrough。
//
// 注意：Gemini Advanced 网页接口使用 Google 内部 RPC 格式（f.req= 编码），
// body 组装由 caller 负责；adapter 仅注入 Auth cookie + 必要 CORS/签名 header。
// endpoint 路径含动态 BL 参数，生产环境需 OCAW 层定期刷新，此处为占位路径。
package gemini

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// defaultGeminiAdvancedEndpoint Gemini 网页前端数据接口占位路径。
// TODO(OCAW): 真实 endpoint 含动态 bl= 参数，需定期采集刷新；
// 参考格式：https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?bl=boq_assistant-bard-web-server_YYYYMMDD.XX_p0&_reqid=NNNNNN&rt=c
const defaultGeminiAdvancedEndpoint = "https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate" // TODO(OCAW): 补充 bl= 动态参数

// defaultGeminiUserAgent 模拟 Chrome 浏览器访问 Gemini 网页的 UA。
const defaultGeminiUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

// 编译期接口合规断言。
var _ provider.Adapter = (*GeminiAdvancedSessionAdapter)(nil)

// GeminiAdvancedSessionAdapter 将 Google Gemini Advanced 网页登录态
// 转换为 HUAKAI 可路由的出站请求。
//
// 与标准 AI Studio API 适配器的区别：
//   - 目标是 gemini.google.com 网页后端，非 generativelanguage.googleapis.com
//   - 凭据为 Google 账户 cookie，非 API key
//   - 需注入 SAPISIDHASH / X-Goog-Authuser 等 Google 内部签名 header
//   - body 为 f.req= 编码的 protobuf-JSON，由 caller 组装
type GeminiAdvancedSessionAdapter struct {
	// Endpoint 允许覆盖默认目标 URL；空值时使用 defaultGeminiAdvancedEndpoint。
	Endpoint string
}

// Platform 返回平台代码，与 transport.ProviderCode 对齐。
func (a *GeminiAdvancedSessionAdapter) Platform() string {
	return "gemini_advanced"
}

// AcceptableCredentialTypes 声明本 adapter 接受的凭据形态。
func (a *GeminiAdvancedSessionAdapter) AcceptableCredentialTypes() []provider.CredentialType {
	return []provider.CredentialType{
		provider.CredentialTypeSessionToken,
		provider.CredentialTypeUpstreamPassthrough,
	}
}

// BuildRequest 组装指向 Gemini 网页后端的出站 HTTP 请求。
//
// 校验顺序：
//  1. apikey 凭据明确拒绝
//  2. 凭据形态白名单检查
//  3. Credential.Value 去空白后非空（Google 登录 cookie 串）
//  4. UpstreamModelID 去空白后非空
//  5. 选取目标 endpoint
//  6. 构造带 Context 的 POST 请求
//  7. 注入 Authorization / Cookie header（Gemini 网页主要靠 cookie 鉴权）
//  8. 注入通用 header
//  9. 注入 Google 特有签名 / 用户定向 header
func (a *GeminiAdvancedSessionAdapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	// 步骤 1：apikey 明确拒绝
	if in.Credential.Type == provider.CredentialTypeAPIKey {
		return nil, errors.New("gemini advanced session: apikey 不适用于网页反转路径——请改用 AI Studio API adapter")
	}
	// 步骤 2：白名单检查
	if !a.acceptsCredential(in.Credential.Type) {
		return nil, fmt.Errorf("gemini advanced session: 凭据形态 %q 不受支持", in.Credential.Type)
	}

	// 步骤 3：凭据值非空（应为 Google cookie 串或封装令牌）
	if strings.TrimSpace(in.Credential.Value) == "" {
		return nil, errors.New("gemini advanced session: 凭据值为空——需提供 Google 登录 cookie 串（__Secure-1PSID 等）")
	}

	// 步骤 4：模型标识非空
	if strings.TrimSpace(in.UpstreamModelID) == "" {
		return nil, errors.New("gemini advanced session: UpstreamModelID 不得为空（如 gemini-1.5-pro-latest）")
	}

	// 步骤 5：确定 endpoint
	target := a.Endpoint
	if target == "" {
		target = defaultGeminiAdvancedEndpoint
	}

	// 步骤 6：构造请求
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(in.InboundBody))
	if err != nil {
		return nil, fmt.Errorf("gemini advanced session: 构造出站请求失败: %w", err)
	}

	// 步骤 7：鉴权注入
	// Gemini 网页主要通过 cookie 鉴权；upstream_passthrough 模式下 Value 含完整 Authorization
	switch in.Credential.Type {
	case provider.CredentialTypeSessionToken:
		// session token 模式下 Value 为 cookie 字符串
		req.Header.Set("Cookie", in.Credential.Value)
	case provider.CredentialTypeUpstreamPassthrough:
		req.Header.Set("Authorization", in.Credential.Value)
		// upstream 模式下 cookie 仍需单独传入
		if ck := in.Credential.Extra["cookie"]; ck != "" {
			req.Header.Set("Cookie", ck)
		}
	}

	// 步骤 8：通用 header
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	req.Header.Set("Accept", "*/*")

	ua := in.Credential.Extra["user_agent"]
	if ua == "" {
		ua = defaultGeminiUserAgent
	}
	req.Header.Set("User-Agent", ua)

	// 步骤 9：Google 特有 header
	// SAPISIDHASH：Google API 请求签名，格式 "时间戳_SHA1(时间戳 SAPISID origin)"
	// TODO(OCAW): SAPISIDHASH 需在请求发起时动态计算，此处仅透传 caller 提供的值
	if sapisidHash := in.Credential.Extra["sapisid_hash"]; sapisidHash != "" {
		req.Header.Set("Authorization", "SAPISIDHASH "+sapisidHash)
	}

	// X-Goog-Authuser：多账户场景下指定目标账户索引（0 表示主账户）
	authUser := in.Credential.Extra["goog_authuser"]
	if authUser == "" {
		authUser = "0"
	}
	req.Header.Set("X-Goog-Authuser", authUser)

	// X-Origin：CORS 来源声明，Gemini 网页反转必要字段
	req.Header.Set("X-Origin", "https://gemini.google.com")

	// TODO(OCAW): 以下 header 需采集真实浏览器流量后由 OCAW 层补全
	// - X-Goog-Visitor-Id：访客标识，影响个性化与速率限制
	// - x-goog-ext-*：Google 内部扩展字段，功能待确认
	// - Referer: https://gemini.google.com/
	// - BL 参数（URL query）：boq_assistant-bard-web-server_* 版本号，需定期刷新

	return req, nil
}

// acceptsCredential 检查凭据形态是否在白名单内。
func (a *GeminiAdvancedSessionAdapter) acceptsCredential(t provider.CredentialType) bool {
	for _, allowed := range a.AcceptableCredentialTypes() {
		if allowed == t {
			return true
		}
	}
	return false
}

// 已阅源文件: /c/HUAKAI/repo/backend/internal/provider/openai/codex_session.go; 通道: claude; 时间: 2026-05-06T00:00:00Z
