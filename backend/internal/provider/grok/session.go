// 包 grok — grok.com 网页 session 反转适配器(GROK-01)。
//
// GrokSessionAdapter 把 grok.com 登录后的 sso cookie 反转成 API 形态出站到
// grok.com/rest/app-chat,注入 grok 网页客户端的浏览器指纹头(静态 Chrome/macOS
// DEFAULT_HEADERS + Origin grok.com + sentry baggage + 伪造 x-statsig-id)与
// Cookie(sso / sso-rw + 可选 cf_clearance,过 Cloudflare 5 秒盾)。
//
// 反封禁:必须开着(Owner 2026-06-08)。仅 session_token / upstream_passthrough
// 凭据(拒 apikey)。Body 由 caller 组装成 grok 网页形态;adapter 透传不重塑。
//
// 注:与 anthropic OAuthSessionAdapter 同样,本 adapter 为 provider 侧就绪件;
// serving 路由 + cf_clearance 凭据接线为后续 slice,在接线前不注册(fail-closed)。
package grok

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

// defaultGrokEndpoint grok.com 网页 app-chat 端点。可在 adapter 字段覆盖。
const defaultGrokEndpoint = "https://grok.com/rest/app-chat/conversations/new"

// grokStatsigID 是 grok 网页客户端的 x-statsig-id 已知可用值。
// grok 的 WAF 校验此头存在/形态而非内容,故沿用已知可用值。
const grokStatsigID = "ZTpUeXBlRXJyb3I6IENhbm5vdCByZWFkIHByb3BlcnRpZXMgb2YgdW5kZWZpbmVkIChyZWFkaW5nICdjaGlsZE5vZGVzJyk="

// grokSentryBaggage 是 grok 网页前端的 sentry baggage 头。
const grokSentryBaggage = "sentry-public_key=b311e0f2690c81f25e2c4cf6d4f7ce1c"

// grokWebUserAgent grok 网页客户端 UA(Chrome 133 / macOS)。
const grokWebUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36"

var _ provider.Adapter = (*GrokSessionAdapter)(nil)

// GrokSessionAdapter 实现 provider.Adapter,把 grok.com 网页 session 反转成 API。
type GrokSessionAdapter struct {
	// Endpoint 覆盖默认 grok.com endpoint。
	Endpoint string
}

// Platform 返回平台标识。
func (a *GrokSessionAdapter) Platform() string { return "grok_web_session" }

// AcceptableCredentialTypes 仅接受 session_token(sso cookie 值)与
// upstream_passthrough(caller 已组装完整 Cookie)。apikey 明确拒绝。
func (a *GrokSessionAdapter) AcceptableCredentialTypes() []provider.CredentialType {
	return []provider.CredentialType{
		provider.CredentialTypeSessionToken,
		provider.CredentialTypeUpstreamPassthrough,
	}
}

func (a *GrokSessionAdapter) acceptsCredential(t provider.CredentialType) bool {
	for _, ok := range a.AcceptableCredentialTypes() {
		if ok == t {
			return true
		}
	}
	return false
}

// BuildRequest 构造出站到 grok.com 的请求:注入浏览器指纹头 + Cookie。
func (a *GrokSessionAdapter) BuildRequest(ctx context.Context, in provider.BuildInput) (*http.Request, error) {
	if in.Credential.Type == provider.CredentialTypeAPIKey {
		return nil, errors.New("grok web session: apikey 凭据不支持; 仅 session_token / upstream_passthrough")
	}
	if !a.acceptsCredential(in.Credential.Type) {
		return nil, fmt.Errorf("grok web session: 不支持的凭据形态 %q", in.Credential.Type)
	}
	if strings.TrimSpace(in.Credential.Value) == "" {
		return nil, errors.New("grok web session: 凭据 Value 为空 (sso token 必填)")
	}

	endpoint := a.Endpoint
	if endpoint == "" {
		endpoint = defaultGrokEndpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(in.InboundBody))
	if err != nil {
		return nil, fmt.Errorf("grok web session: 构造请求失败: %w", err)
	}

	applyGrokWebHeaders(req.Header)
	req.Header.Set("Cookie", buildGrokCookie(in.Credential))
	return req, nil
}

// applyGrokWebHeaders 注入 grok 网页客户端的静态浏览器指纹头。
func applyGrokWebHeaders(h http.Header) {
	h.Set("Accept", "*/*")
	h.Set("Accept-Language", "zh-CN,zh;q=0.9")
	h.Set("Content-Type", "text/plain;charset=UTF-8")
	h.Set("Origin", "https://grok.com")
	h.Set("Priority", "u=1, i")
	h.Set("User-Agent", grokWebUserAgent)
	h.Set("Sec-Ch-Ua", `"Not(A:Brand";v="99", "Google Chrome";v="133", "Chromium";v="133"`)
	h.Set("Sec-Ch-Ua-Mobile", "?0")
	h.Set("Sec-Ch-Ua-Platform", `"macOS"`)
	h.Set("Sec-Fetch-Dest", "empty")
	h.Set("Sec-Fetch-Mode", "cors")
	h.Set("Sec-Fetch-Site", "same-origin")
	h.Set("Baggage", grokSentryBaggage)
	h.Set("X-Statsig-Id", grokStatsigID)
}

// buildGrokCookie 组装 grok Cookie。upstream_passthrough 时 Value 即完整 Cookie;
// session_token 时 Value 是 sso 值,组装 sso/sso-rw,并可选拼 Extra["cf_clearance"]
// (过 Cloudflare 5 秒盾;IP 绑定,运营提供)。
func buildGrokCookie(cred provider.Credential) string {
	if cred.Type == provider.CredentialTypeUpstreamPassthrough {
		return cred.Value
	}
	sso := strings.TrimSpace(cred.Value)
	parts := []string{"sso=" + sso, "sso-rw=" + sso}
	if cf := strings.TrimSpace(cred.Extra["cf_clearance"]); cf != "" {
		if strings.HasPrefix(cf, "cf_clearance=") {
			parts = append(parts, cf)
		} else {
			parts = append(parts, "cf_clearance="+cf)
		}
	}
	return strings.Join(parts, "; ")
}
