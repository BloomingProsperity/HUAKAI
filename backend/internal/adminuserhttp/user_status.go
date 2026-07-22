package adminuserhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

// user_status.go — 终端用户封禁/启用(slice 3,从 routes.go 抽出以守软预算门)。
type setUserStatusRequest struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// postgresUserStatusStore 设 users.status(封禁/启用)。raw pgx 同 user-group/remark
// store(sqlc v1.27 工具链漂移,见 tenancy slice1);WHERE 含 deleted_at IS NULL
// 防把软删用户改回 active。
type postgresUserStatusStore struct {
	pool *pgxpool.Pool
}

// NewPostgresUserStatusStore 接线 admin 用户状态 setter。
func NewPostgresUserStatusStore(pool *pgxpool.Pool) userStatusSetter {
	if pool == nil {
		return nil
	}
	return postgresUserStatusStore{pool: pool}
}

func (s postgresUserStatusStore) SetUserStatusForTenant(ctx context.Context, tenantID, userID int64, status string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET status=$3, updated_at=now() WHERE tenant_id=$1 AND id=$2 AND principal_kind='human' AND deleted_at IS NULL`, tenantID, userID, status)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
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
		if d.UserStatusSetter == nil || d.Audit == nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_not_configured", "admin user-status dependency unset")
			return
		}
		before, err := d.Store.AdminGetUserForTenant(r.Context(), admindb.AdminGetUserForTenantParams{TenantID: tenantID, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "admin_user_not_found", "user not found")
			return
		} else if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error", fmt.Sprintf("get user failed: %v", err))
			return
		}
		affected, err := d.UserStatusSetter.SetUserStatusForTenant(r.Context(), tenantID, userID, status)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_user_status_failed", fmt.Sprintf("set user status failed: %v", err))
			return
		}
		if affected == 0 {
			// AdminGetUserForTenant 命中但 UPDATE 0 行 = 该用户已软删(deleted_at);
			// 不静默成功(否则运营者以为封了实际没动)。
			writeError(w, http.StatusNotFound, "admin_user_not_found", "user not found or already deleted")
			return
		}
		// 封禁第三轴:撤既有会话(bearer+refresh)。登录门与 API key 联查只挡新入口,
		// 已签发的 self-service 会话不撤就能活到自然过期。与删除路径同纪律:失败映 503
		// 让调用者重试(RevokeUser 幂等);重新启用(active)不撤。
		if status == "disabled" && d.SessionRevoker != nil {
			if _, err := d.SessionRevoker.Revoke(r.Context(), usersession.RevokeInput{
				TenantID: tenantID, UserID: userID, Reason: "admin_user_disabled",
			}); err != nil {
				writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
					fmt.Sprintf("revoke disabled user sessions failed: %v", err))
				return
			}
		}
		ai := buildUnlockAuditInput(r, ident, before.Status)
		payload, err := marshalUnlockAuditPayload(before.Status, status)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error", fmt.Sprintf("marshal status audit failed: %v", err))
			return
		}
		audit := admindb.InsertAdminAuditEventParams{
			TenantID: &tenantID, ActorID: ai.ActorID, ActorRole: ai.ActorRole,
			Action: "set_user_status", TargetType: "user", TargetID: &userID, RequestID: ai.RequestID,
			Payload: payload,
		}
		if _, err := d.Audit.InsertAdminAuditEvent(r.Context(), audit); err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error", fmt.Sprintf("write status audit failed: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": userID, "status": status})
	}
}
