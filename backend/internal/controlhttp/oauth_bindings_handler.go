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
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

// OAuthBindingLister 读取已认证用户自己的社交登录绑定(只读),由 *userauth.Service 实现。
type OAuthBindingLister interface {
	ListSocialIdentityLinks(ctx context.Context, tenantID, userID int64) ([]userauth.SocialIdentityLink, error)
}

// OAuthBindingsDeps 是 /v1/users/me/oauth-bindings 路由的依赖。两端点均由 *userauth.Service 满足。
type OAuthBindingsDeps struct {
	// Bindings 列出本人绑定(只读)。nil = 列表端点未配置。
	Bindings OAuthBindingLister
	// SocialLinks 解绑(末位登录方式保护在 service 层)。nil = 解绑端点未配置。
	SocialLinks AuthSocialLinkService
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
