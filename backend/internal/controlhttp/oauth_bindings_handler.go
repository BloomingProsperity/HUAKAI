// Package controlhttp · oauth_bindings_handler.go
//
// 已登录用户自助查看 / 解绑自己的社交登录(OAuth)绑定。两端点都挂在 /v1/users/me/oauth-bindings 的
// session 中间件下,tenant/user 只取已认证 session,绝不信请求路径 / 查询参数 / 请求体——避免越权读写他人绑定。
//
//	GET    /v1/users/me/oauth-bindings            列出本人绑定(provider + 脱敏 subject + linked_at)
//	DELETE /v1/users/me/oauth-bindings/{provider}  解绑指定 provider(末位登录方式由 service 保护 → 409)
package controlhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/telegramauth"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

// OAuthBindingLister 读取已认证用户自己的社交登录绑定(只读),由 *userauth.Service 实现。
type OAuthBindingLister interface {
	ListSocialIdentityLinks(ctx context.Context, tenantID, userID int64) ([]userauth.SocialIdentityLink, error)
}

// VerifiedSocialBinder 把一个已校验的社交身份绑定到已登录用户(「先绑定后登录」的绑定腿),
// 由 *userauth.Service 实现。接管保护在 service 层(身份已绑他人 → ErrSocialIdentityAlreadyBound)。
type VerifiedSocialBinder interface {
	LinkVerifiedSocialIdentity(ctx context.Context, tenantID, userID int64, identity userauth.VerifiedIdentity) (userauth.User, error)
}

// OAuthBindingsDeps 是 /v1/users/me/oauth-bindings 路由的依赖。列表/解绑由 *userauth.Service 满足。
type OAuthBindingsDeps struct {
	// Bindings 列出本人绑定(只读)。nil = 列表端点未配置。
	Bindings OAuthBindingLister
	// SocialLinks 解绑(末位登录方式保护在 service 层)。nil = 解绑端点未配置。
	SocialLinks AuthSocialLinkService
	// TelegramBinder 绑定 telegram 身份到本人。nil 或 TelegramBotToken 为空 = telegram 绑定端点未启用。
	TelegramBinder VerifiedSocialBinder
	// TelegramBotToken 是静态 HMAC 校验密钥(测试/旧装配)。生产走 TelegramBotTokenResolver 请求期读
	// 后台设置(settings-first,空回退 env),与登录端点同源。
	TelegramBotToken         string
	TelegramBotTokenResolver func(context.Context) string
	// TelegramWidgetMaxAge 是 widget auth_date 的最大有效期;<=0 时 handler 施加安全默认 24h
	// (见 telegramWidgetMaxAge),绝不把 0 直传 verifier —— 否则年龄门 `if maxAge>0` 永不触发,
	// 已签名载荷可无限期重放。
	TelegramWidgetMaxAge time.Duration
}

// defaultTelegramWidgetMaxAge 是 widget auth_date 的兜底有效期:装配方未显式配置(<=0)时启用,
// 与登录端点(gatewayhttp.telegramWidgetMaxAge)同值,防止已签名 widget 无限重放。
const defaultTelegramWidgetMaxAge = 24 * time.Hour

// telegramWidgetMaxAge 返回实际生效的 widget 时效:显式正值优先,否则回退安全默认。
// 由此保证即便装配处漏配 TelegramWidgetMaxAge,verifier 也会收到 >0 的门限并拒绝过期载荷。
func (d OAuthBindingsDeps) telegramWidgetMaxAge() time.Duration {
	if d.TelegramWidgetMaxAge > 0 {
		return d.TelegramWidgetMaxAge
	}
	return defaultTelegramWidgetMaxAge
}

// resolveTelegramBotToken 请求期解析 bot token:优先 resolver(后台设置),否则静态字段。
func (d OAuthBindingsDeps) resolveTelegramBotToken(ctx context.Context) string {
	if d.TelegramBotTokenResolver != nil {
		if v := strings.TrimSpace(d.TelegramBotTokenResolver(ctx)); v != "" {
			return v
		}
	}
	return d.TelegramBotToken
}

// oauthBindingResponse 是单条绑定的出网 DTO。subject 已在 service 层脱敏;不含上游 OAuth token。
type oauthBindingResponse struct {
	Provider string `json:"provider"`
	Subject  string `json:"subject"`
	LinkedAt string `json:"linked_at"`
}

// MountOAuthBindingsRoutes 挂载 /v1/users/me/oauth-bindings 子路由(相对路径)。调用方必须先在
// /v1/users/me/oauth-bindings 路由组上套 session 中间件,故此处用 "/" 与 "/{provider}" 相对挂载。
func MountOAuthBindingsRoutes(r chi.Router, d OAuthBindingsDeps) {
	r.Get("/", newOAuthBindingsListHandler(d))
	r.Delete("/{provider}", newOAuthBindingsUnlinkHandler(d))
	// 绑定 telegram(「先绑定后登录」的绑定腿):已登录用户用 Telegram Login Widget 回传数据绑定自己的
	// telegram 身份;绑定后才能在登录页用 telegram 直接登录(见 userauth.applyVerifiedSocialIdentity 既有绑定优先)。
	r.Post("/telegram", newOAuthBindingsTelegramHandler(d))
}

// oauthBindingsTelegramRequest 是绑定 telegram 的请求体。params 即 Telegram Login Widget 回传的字段集
// (id/first_name/last_name/username/photo_url/auth_date/hash);tenant/user 绝不取自请求体,只取 session。
type oauthBindingsTelegramRequest struct {
	Params map[string]string `json:"params"`
}

func newOAuthBindingsTelegramHandler(d OAuthBindingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		botToken := d.resolveTelegramBotToken(r.Context())
		if d.TelegramBinder == nil || strings.TrimSpace(botToken) == "" {
			controlWriteJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "telegram binding not configured")
			return
		}
		ident, ok := authMeSessionIdentity(w, r)
		if !ok {
			return
		}
		var req oauthBindingsTelegramRequest
		defer r.Body.Close()
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			controlWriteJSONError(w, http.StatusBadRequest, "invalid_telegram_binding_request", "invalid JSON body")
			return
		}
		// 服务端用 bot token HMAC 校验 widget 数据;客户端传来的任何字段都不被信任(信任靠签名)。
		identity, err := telegramauth.VerifyWidget(req.Params, botToken, time.Now(), d.telegramWidgetMaxAge())
		if err != nil {
			// 校验失败统一按 social_identity_verification_failed(401)处理,不回显细节。
			writeAuthSocialLinkError(w, userauth.ErrSocialLoginRejected)
			return
		}
		// tenant/user 取自 session;接管保护(已绑他人 → 409)在 service 层。
		if _, err := d.TelegramBinder.LinkVerifiedSocialIdentity(r.Context(), ident.TenantID, ident.UserID, identity); err != nil {
			writeAuthSocialLinkError(w, err)
			return
		}
		controlWriteJSON(w, http.StatusOK, map[string]any{"status": "bound", "provider": userauth.SocialProviderTelegram})
	}
}

func newOAuthBindingsListHandler(d OAuthBindingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Bindings == nil {
			controlWriteJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "oauth bindings dependency unset")
			return
		}
		ident, ok := authMeSessionIdentity(w, r)
		if !ok {
			return
		}
		// tenant/user 只取 session:即便 path/query/body 夹带 user_id 也忽略,store 收到的永远是 session 身份。
		links, err := d.Bindings.ListSocialIdentityLinks(r.Context(), ident.TenantID, ident.UserID)
		if err != nil {
			writeAuthSocialLinkError(w, err)
			return
		}
		out := make([]oauthBindingResponse, 0, len(links))
		for _, link := range links {
			out = append(out, oauthBindingResponse{
				Provider: link.Provider,
				Subject:  link.Subject,
				LinkedAt: link.LinkedAt.UTC().Format(http.TimeFormat),
			})
		}
		controlWriteJSON(w, http.StatusOK, map[string]any{"bindings": out})
	}
}

func newOAuthBindingsUnlinkHandler(d OAuthBindingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.SocialLinks == nil {
			controlWriteJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "social link dependency unset")
			return
		}
		ident, ok := authMeSessionIdentity(w, r)
		if !ok {
			return
		}
		provider := chi.URLParam(r, "provider")
		// service 层负责末位登录方式保护(无密码且这是唯一绑定 → ErrLastLoginMethod → 409),
		// 以及 not-linked → unlinked=false(200 no-op,与既有 /account-bindings 解绑约定一致)。
		unlinked, err := d.SocialLinks.UnlinkSocialIdentity(r.Context(), ident.TenantID, ident.UserID, provider)
		if err != nil {
			writeAuthSocialLinkError(w, err)
			return
		}
		controlWriteJSON(w, http.StatusOK, map[string]any{"unlinked": unlinked})
	}
}
