package adminuserhttp

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
)

// user_delete.go — admin 软删终端用户(S4)。软删(deleted_at=now,status='deleted')
// 非硬删,对齐 users 的 uq_users_tenant_email partial-index(WHERE deleted_at IS NULL),
// 使删后同邮箱可复建。删除护栏拒绝 target.role=='admin'，禁止经本面删除
// admin。删除连带撤会话(usersession RevokeUser)——封号即时生效语义,否则被删
// 用户持已签 session 仍可访问(越权窗口)。API key 失效由 api_key_resolver 联查
// user_status!=active 天然达成(status='deleted'),无需显式撤 key。

// newDeleteUserHandler 软删终端用户(DELETE /admin/v1/users/{id})。
func newDeleteUserHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveTenantIdentity(w, r, d)
		if !ok {
			return
		}
		userID, ok := pathID(w, r)
		if !ok {
			return
		}
		if d.UserMutations == nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_not_configured",
				"admin user-delete dependency unset")
			return
		}
		sessionsRevoked, err := d.UserMutations.SoftDeleteUserWithAudit(
			r.Context(), tenantID, userID, buildUnlockAuditInput(r, ident, ""),
		)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "admin_user_not_found", "user not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
				fmt.Sprintf("delete user failed: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":               userID,
			"deleted":          true,
			"sessions_revoked": sessionsRevoked,
		})
	}
}
