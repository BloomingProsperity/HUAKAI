// Package controlhttp · self_account_handler.go
//
// 已登录用户自助账户管理:改密(校旧密)+ 删号(软删)。两端点都挂在 /v1/auth/me/* 的
// session 中间件下,身份(tenant/user/family)只取已认证 session,绝不信请求体。
package controlhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

// SelfAccountService 改密 + 软删,由 *userauth.Service 实现。
type SelfAccountService interface {
	ChangeOwnPassword(ctx context.Context, tenantID, userID int64, oldPassword, newPassword string) (userauth.User, error)
	ChangeOwnPasswordAndRevokeOthers(
		ctx context.Context,
		tenantID, userID int64,
		oldPassword, newPassword, currentFamilyID string,
	) (userauth.User, int64, error)
	SoftDeleteSelf(ctx context.Context, tenantID, userID int64) (userauth.User, error)
	SoftDeleteSelfAndRevokeSessions(ctx context.Context, tenantID, userID int64) (userauth.User, int64, error)
}

type changePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// newAuthChangePasswordHandler POST /v1/auth/me/password
// 校旧密 → 改新密(bump password_version)→ 撤其它 session、保留当前。
func newAuthChangePasswordHandler(d AuthMeDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.SelfAccount == nil {
			controlWriteJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "self-account dependency unset")
			return
		}
		ident, ok := authMeSessionIdentity(w, r)
		if !ok {
			return
		}
		// 改密必须能保留当前 session(RevokeOthers 需 CurrentFamilyID);无 family 的 session
		// 无法精确「撤其它留当前」,拒绝以免误把自己也撤掉或撤不干净。
		if strings.TrimSpace(ident.FamilyID) == "" {
			controlWriteJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
			return
		}
		var req changePasswordRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
		if err := decoder.Decode(&req); err != nil {
			controlWriteJSONError(w, http.StatusBadRequest, "invalid_request_body", "request body must be JSON")
			return
		}
		// 早返:new_password 空一律 400,service 不被调(避免无谓的 argon2 / DB 触碰)。
		if strings.TrimSpace(req.NewPassword) == "" {
			controlWriteJSONError(w, http.StatusBadRequest, "invalid_password", "new_password must be non-empty and satisfy the password policy")
			return
		}
		// 身份只取 session:即便 body 夹带 user_id 等也忽略,service 收到的永远是 session 身份。
		_, revoked, err := d.SelfAccount.ChangeOwnPasswordAndRevokeOthers(
			r.Context(),
			ident.TenantID,
			ident.UserID,
			req.OldPassword,
			req.NewPassword,
			ident.FamilyID,
		)
		if err != nil {
			writeAuthChangePasswordError(w, err)
			return
		}
		controlWriteJSON(w, http.StatusOK, map[string]any{"changed": true, "sessions_revoked": revoked})
	}
}

// newAuthDeleteSelfHandler DELETE /v1/auth/me
// 软删本人(末位 admin 保护在 service/store 层)→ 撤本人全部 session(api_key 已在软删事务内失活)。
func newAuthDeleteSelfHandler(d AuthMeDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.SelfAccount == nil {
			controlWriteJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "self-account dependency unset")
			return
		}
		ident, ok := authMeSessionIdentity(w, r)
		if !ok {
			return
		}
		_, revoked, err := d.SelfAccount.SoftDeleteSelfAndRevokeSessions(
			r.Context(),
			ident.TenantID,
			ident.UserID,
		)
		if err != nil {
			writeAuthDeleteSelfError(w, err)
			return
		}
		controlWriteJSON(w, http.StatusOK, map[string]any{"deleted": true, "sessions_revoked": revoked})
	}
}

func writeAuthChangePasswordError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, userauth.ErrInvalidCredentials):
		// 旧密错:用 401(非 403)+ generic 消息,与既有 reset / login 路径的脱敏纪律一致。
		controlWriteJSONError(w, http.StatusUnauthorized, "invalid_old_password", "the current password is incorrect")
	case errors.Is(err, userauth.ErrInvalidInput):
		controlWriteJSONError(w, http.StatusBadRequest, "invalid_password", "new_password must be non-empty and satisfy the password policy")
	case errors.Is(err, userauth.ErrUserNotFound):
		controlWriteJSONError(w, http.StatusForbidden, "account_not_active", "account is no longer active")
	default:
		controlWriteJSONError(w, http.StatusServiceUnavailable, "password_backend_error", "password backend unavailable")
	}
}

func writeAuthDeleteSelfError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, userauth.ErrLastAdmin):
		controlWriteJSONError(w, http.StatusConflict, "last_admin_protected", "cannot delete the last administrator account")
	case errors.Is(err, userauth.ErrUserNotFound):
		// 已被软删(幂等并发第二次)/ 账号不存在 → 视作不再活跃。
		controlWriteJSONError(w, http.StatusForbidden, "account_not_active", "account is no longer active")
	default:
		controlWriteJSONError(w, http.StatusServiceUnavailable, "account_backend_error", "account backend unavailable")
	}
}
