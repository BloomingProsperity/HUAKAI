package adminuserhttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

// user_status.go — 终端用户封禁/启用(slice 3,从 routes.go 抽出以守软预算门)。
type setUserStatusRequest struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// newSetUserStatusHandler 封禁/启用终端用户(users.status active<->disabled)。
// 封号即时生效双轴:既挡登录(userauth),也挡已签发 API key
// (auth.api_key_resolver 联查 user_status != active)。
// 'deleted' 不经本端点(破坏性删除是独立操作);租户隔离 + 审计(set_user_status)。
// 运营者经 admin_token 鉴权(独立于 user 表),封号不自锁。
func newSetUserStatusHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveTenantIdentity(w, r, d)
		if !ok {
			return
		}
		userID, ok := pathID(w, r)
		if !ok {
			return
		}
		var req setUserStatusRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		status := strings.TrimSpace(req.Status)
		// 只接受封禁/启用二态;'deleted'/'locked' 等不经本端点(破坏性删除单独走,
		// locked 是登录失败锁定的内部态,不由 admin 直设)。
		if status != "active" && status != "disabled" {
			writeError(w, http.StatusBadRequest, "invalid_status",
				"status must be 'active' or 'disabled'")
			return
		}
		if d.UserMutations == nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_not_configured", "admin user-status dependency unset")
			return
		}
		sessionsRevoked, err := d.UserMutations.SetUserStatusWithAudit(
			r.Context(), tenantID, userID, status, req.Reason, buildUnlockAuditInput(r, ident, ""),
		)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "admin_user_not_found", "user not found")
			return
		}
		if errors.Is(err, errUserStatusTransitionConflict) {
			writeError(w, http.StatusConflict, "admin_user_status_conflict",
				"user recovery state must be resolved by its dedicated flow")
			return
		}
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_user_status_failed", fmt.Sprintf("set user status failed: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":               userID,
			"status":           status,
			"sessions_revoked": sessionsRevoked,
		})
	}
}
