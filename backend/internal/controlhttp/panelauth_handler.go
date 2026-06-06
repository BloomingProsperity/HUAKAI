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
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

// PanelResolver 解析用户面板归属(由 *panelauth.Resolver 实现)。
type PanelResolver interface {
	PanelForUser(ctx context.Context, tenantID, userID int64) (panelauth.Panel, error)
}

// AuthProfileService reads and updates the authenticated user's own profile.
type AuthProfileService interface {
	GetProfile(ctx context.Context, tenantID, userID int64) (userauth.User, error)
	UpdateProfile(ctx context.Context, tenantID, userID int64, displayName string) (userauth.User, error)
}

// AuthMeDeps whoami 路由依赖。
type AuthMeDeps struct {
	Resolver PanelResolver
	Profiles AuthProfileService
}

// meResponse 是 /auth/me 的响应 DTO — 仅暴露面板归属与自身 id, 不含任何敏感字段。
type meResponse struct {
	Panel       string `json:"panel"`
	UserID      int64  `json:"user_id"`
	TenantID    int64  `json:"tenant_id"`
	DisplayName string `json:"display_name"`
}

type updateProfileRequest struct {
	DisplayName string `json:"display_name"`
}

type profileResponse struct {
	UserID      int64  `json:"user_id"`
	TenantID    int64  `json:"tenant_id"`
	DisplayName string `json:"display_name"`
}

// MountAuthMeRoutes 挂载 /me 自助资料路由。调用方必须先套 session 中间件。
func MountAuthMeRoutes(r chi.Router, d AuthMeDeps) {
	r.Get("/me", newAuthMeHandler(d))
	r.Put("/me/profile", newAuthProfileUpdateHandler(d))
}

func newAuthMeHandler(d AuthMeDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Resolver == nil || d.Profiles == nil {
			controlWriteJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "panel auth dependency unset")
			return
		}
		ident, ok := authMeSessionIdentity(w, r)
		if !ok {
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
		user, err := d.Profiles.GetProfile(r.Context(), ident.TenantID, ident.UserID)
		if err != nil {
			writeAuthProfileError(w, err, "profile_read_failed")
			return
		}
		controlWriteJSON(w, http.StatusOK, meResponse{Panel: string(panel), UserID: ident.UserID, TenantID: ident.TenantID, DisplayName: user.DisplayName})
	}
}

func newAuthProfileUpdateHandler(d AuthMeDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Profiles == nil {
			controlWriteJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "profile dependency unset")
			return
		}
		ident, ok := authMeSessionIdentity(w, r)
		if !ok {
			return
		}
		var req updateProfileRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
		if err := decoder.Decode(&req); err != nil {
			controlWriteJSONError(w, http.StatusBadRequest, "invalid_request_body", "request body must be JSON")
			return
		}
		user, err := d.Profiles.UpdateProfile(r.Context(), ident.TenantID, ident.UserID, req.DisplayName)
		if err != nil {
			writeAuthProfileError(w, err, "profile_update_failed")
			return
		}
		controlWriteJSON(w, http.StatusOK, profileResponse{UserID: user.ID, TenantID: user.TenantID, DisplayName: user.DisplayName})
	}
}

func authMeSessionIdentity(w http.ResponseWriter, r *http.Request) (sessionauth.SessionIdentity, bool) {
	ident, ok := sessionauth.SessionFromContext(r.Context())
	if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
		controlWriteJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
		return sessionauth.SessionIdentity{}, false
	}
	return ident, true
}

func writeAuthProfileError(w http.ResponseWriter, err error, fallbackCode string) {
	switch {
	case errors.Is(err, userauth.ErrInvalidInput):
		controlWriteJSONError(w, http.StatusBadRequest, "invalid_display_name", "display_name must be 1-100 characters without control characters")
	case errors.Is(err, userauth.ErrUserNotFound):
		controlWriteJSONError(w, http.StatusForbidden, "account_not_active", "account is no longer active")
	default:
		controlWriteJSONError(w, http.StatusServiceUnavailable, fallbackCode, "profile backend unavailable")
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
