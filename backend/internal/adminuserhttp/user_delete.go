package adminuserhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

// user_delete.go — admin 软删终端用户(S4)。软删(deleted_at=now,status='deleted')
// 非硬删,对齐 users 的 uq_users_tenant_email partial-index(WHERE deleted_at IS NULL),
// 使删后同邮箱可复建。删除护栏:拒 target.role=='admin'(对齐 sub2api,禁经本面删
// admin)。删除连带撤会话(usersession RevokeUser)——封号即时生效语义,否则被删
// 用户持已签 session 仍可访问(越权窗口)。API key 失效由 api_key_resolver 联查
// user_status!=active 天然达成(status='deleted'),无需显式撤 key。

// userSoftDeleteService 软删用户,返回受影响行数(0 = 不存在或已软删 → 404)。
type userSoftDeleteService interface {
	SoftDeleteForTenant(ctx context.Context, tenantID, userID int64) (int64, error)
}

// userSessionRevoker 撤某用户全部会话族 + refresh + session token。由
// usersession.Service 满足(Revoke 在仅 UserID>0 时落到 Store.RevokeUser)。
type userSessionRevoker interface {
	Revoke(ctx context.Context, in usersession.RevokeInput) (int64, error)
}

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
		if d.UserSoftDeleter == nil || d.Audit == nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_not_configured",
				"admin user-delete dependency unset")
			return
		}
		before, err := d.Store.AdminGetUserForTenant(r.Context(), admindb.AdminGetUserForTenantParams{TenantID: tenantID, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "admin_user_not_found", "user not found")
			return
		} else if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
				fmt.Sprintf("get user failed: %v", err))
			return
		}
		// 越权护栏(CMB-5 类):本面绝不能删 role='admin' 用户(对齐 sub2api 的
		// 「cannot delete admin user」)。在 SoftDelete 之前拒,setter 不被调。
		if before.Role == "admin" {
			writeError(w, http.StatusForbidden, "admin_cannot_delete_admin",
				"cannot delete an admin user via this endpoint")
			return
		}
		affected, err := d.UserSoftDeleter.SoftDeleteForTenant(r.Context(), tenantID, userID)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
				fmt.Sprintf("delete user failed: %v", err))
			return
		}
		if affected == 0 {
			// AdminGetUserForTenant 命中但 UPDATE 0 行 = 该用户已软删(竞态)或
			// 已被删;不静默成功(否则运营者以为删了实际没动)。
			writeError(w, http.StatusNotFound, "admin_user_not_found", "user not found or already deleted")
			return
		}
		// 撤会话:删后立即切断已签 session(否则被删用户持 cookie/token 仍可访问)。
		// Revoke 失败不回滚软删(软删已是封号语义,会话撤销是加固);但记日志路径
		// 不在本切片——失败映 503 让调用者重试(幂等:RevokeUser 对已撤会话无副作用)。
		if d.SessionRevoker != nil {
			if _, err := d.SessionRevoker.Revoke(r.Context(), usersession.RevokeInput{
				TenantID: tenantID, UserID: userID, Reason: "admin_user_deleted",
			}); err != nil {
				writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
					fmt.Sprintf("revoke deleted user sessions failed: %v", err))
				return
			}
		}
		ai := buildUnlockAuditInput(r, ident, before.Status)
		// admin_audit_events.payload is NOT NULL; record the prior status so the
		// audit row is self-describing (and the insert does not 23502).
		payload, err := json.Marshal(map[string]string{"prior_status": before.Status})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
				fmt.Sprintf("marshal delete audit failed: %v", err))
			return
		}
		audit := admindb.InsertAdminAuditEventParams{
			TenantID: &tenantID, ActorID: ai.ActorID, ActorRole: ai.ActorRole,
			Action: "delete_user", TargetType: "user", TargetID: &userID, RequestID: ai.RequestID,
			Payload: payload,
		}
		if _, err := d.Audit.InsertAdminAuditEvent(r.Context(), audit); err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
				fmt.Sprintf("write delete audit failed: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": userID, "deleted": true})
	}
}

// postgresUserSoftDeleteStore 软删 users 行(deleted_at=now,status='deleted'),
// WHERE deleted_at IS NULL 保证幂等:重复删返 0 行 → 404。raw pgx 同 user-group/
// status store(sqlc 工具链漂移)。
type postgresUserSoftDeleteStore struct {
	pool *pgxpool.Pool
}

// NewPostgresUserSoftDeleteStore 接线 admin 用户软删 store。
func NewPostgresUserSoftDeleteStore(pool *pgxpool.Pool) userSoftDeleteService {
	if pool == nil {
		return nil
	}
	return postgresUserSoftDeleteStore{pool: pool}
}

func (s postgresUserSoftDeleteStore) SoftDeleteForTenant(ctx context.Context, tenantID, userID int64) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE users SET deleted_at=now(), status='deleted', updated_at=now()
		 WHERE tenant_id=$1 AND id=$2 AND deleted_at IS NULL`,
		tenantID, userID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
