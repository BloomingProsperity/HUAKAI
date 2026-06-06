// HUAKAI · iKun

// Package controlhttp 暴露 /v1/auth/me(whoami): 已登录用户查询自己的面板归属
// (admin / user)。挂载在 session 中间件下, 角色来自后端解析(users.role), 绝不信前端。
// handler 由 cmd/gateway/routes.go 挂载。
package controlhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/panelauth"
)

// PanelResolver 解析用户面板归属(由 *panelauth.Resolver 实现)。
type PanelResolver interface {
	PanelForUser(ctx context.Context, tenantID, userID int64) (panelauth.Panel, error)
}

// AuthMeDeps whoami 路由依赖。
type AuthMeDeps struct {
	Resolver PanelResolver
}

// meResponse 是 /auth/me 的响应 DTO — 仅暴露面板归属与自身 id, 不含任何敏感字段。
type meResponse struct {
	Panel    string `json:"panel"`
	UserID   int64  `json:"user_id"`
	TenantID int64  `json:"tenant_id"`
}

// MountAuthMeRoutes 挂载 GET /me。调用方必须先套 session 中间件(本路由依赖已认证 session)。
func MountAuthMeRoutes(r chi.Router, d AuthMeDeps) {
	r.Get("/me", newAuthMeHandler(d))
}

func newAuthMeHandler(d AuthMeDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Resolver == nil {
			controlWriteJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "panel auth dependency unset")
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok {
			controlWriteJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
			return
		}
		// 面板归属由后端按账号 role 解析, 取自已认证 session 的 tenant/user, 绝不取自请求体或前端声明。
		panel, err := d.Resolver.PanelForUser(r.Context(), ident.TenantID, ident.UserID)
		if err != nil {
			if errors.Is(err, panelauth.ErrUserNotFound) {
				// session 有效但用户行不存在/已软删 → 账号已注销, 拒发任何面板(deny-by-default)。
				controlWriteJSONError(w, http.StatusForbidden, "account_not_active", "account is no longer active")
				return
			}
			controlWriteJSONError(w, http.StatusServiceUnavailable, "panel_backend_error", "panel resolution unavailable")
			return
		}
		controlWriteJSON(w, http.StatusOK, meResponse{Panel: string(panel), UserID: ident.UserID, TenantID: ident.TenantID})
	}
}

func controlWriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func controlWriteJSONError(w http.ResponseWriter, status int, code, message string) {
	controlWriteJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
