// Package adminuserhttp 暴露按租户隔离的 admin 用户可见性与账号恢复端点。
package adminuserhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

const (
	defaultPageLimit = int32(50)
	maxPageLimit     = int32(100)
)

type Deps struct {
	Auth             adminAuth
	Store            userReadStore
	UsageStore       UsageStore
	SocialLinks      socialLinkService
	UnlockAudit      userUnlockAuditStore
	Unlocker         userUnlockService
	Audit            adminAuditStore
	TwoFADisabler    twoFADisableService
	PasskeyResetter  passkeyResetService
	UserGroupSetter  userGroupSetter
	UserRemarkSetter userRemarkSetter
	UserStatusSetter userStatusSetter
	UserCreator      userCreateService
	UserSoftDeleter  userSoftDeleteService
	SessionRevoker   userSessionRevoker
}

type adminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type userReadStore interface {
	AdminListUsersForTenant(context.Context, admindb.AdminListUsersForTenantParams) ([]admindb.AdminListUsersForTenantRow, error)
	AdminGetUserForTenant(context.Context, admindb.AdminGetUserForTenantParams) (admindb.AdminGetUserForTenantRow, error)
	AdminGetTwoFAAdoptionStatsForTenant(context.Context, int64) (admindb.AdminGetTwoFAAdoptionStatsForTenantRow, error)
	AdminListUserBalanceHistoryForTenant(context.Context, admindb.AdminListUserBalanceHistoryForTenantParams) ([]admindb.AdminListUserBalanceHistoryForTenantRow, error)
}

type socialLinkService interface {
	UnlinkSocialIdentity(context.Context, int64, int64, string) (bool, error)
}

type userUnlockService interface {
	UnlockUser(context.Context, int64, int64) (userauth.User, error)
}

type userUnlockAuditStore interface {
	UnlockUserWithAudit(context.Context, int64, int64, unlockAuditInput) (userauth.User, error)
}

type adminAuditStore interface {
	InsertAdminAuditEvent(context.Context, admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error)
}

type twoFADisableService interface {
	Disable(ctx context.Context, tenantID, userID int64) error
}

type passkeyResetService interface {
	AdminClearCredentials(ctx context.Context, tenantID, userID int64) (int, error)
}

type userGroupSetter interface {
	SetUserGroupForTenant(ctx context.Context, tenantID, userID int64, group string) error
}

type userRemarkSetter interface {
	SetUserRemarkForTenant(ctx context.Context, tenantID, userID int64, remark string) error
}

type userStatusSetter interface {
	// SetUserStatusForTenant 设 users.status;返回受影响行数(0 = 该租户无此用户)
	// 供 handler 区分 404 与成功,避免「改了别租户/不存在的用户却报成功」。
	SetUserStatusForTenant(ctx context.Context, tenantID, userID int64, status string) (int64, error)
}

type unlockAuditInput struct {
	ActorID      string
	ActorRole    string
	RequestID    *string
	BeforeStatus string
}

type postgresUnlockAuditStore struct {
	pool *pgxpool.Pool
}

func NewPostgresUnlockAuditStore(pool *pgxpool.Pool) userUnlockAuditStore {
	if pool == nil {
		return nil
	}
	return postgresUnlockAuditStore{pool: pool}
}

func (s postgresUnlockAuditStore) UnlockUserWithAudit(ctx context.Context, tenantID, userID int64, input unlockAuditInput) (userauth.User, error) {
	if s.pool == nil {
		return userauth.User{}, userauth.ErrStoreNotConfigured
	}
	var updated userauth.User
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		user, err := userauth.NewPostgresStore(tx).ClearLockout(ctx, tenantID, userID)
		if err != nil {
			return err
		}
		payload, err := marshalUnlockAuditPayload(input.BeforeStatus, string(user.Status))
		if err != nil {
			return err
		}
		if _, err := admindb.New(tx).InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
			TenantID:   &tenantID,
			ActorID:    input.ActorID,
			ActorRole:  input.ActorRole,
			Action:     "unlock_user",
			TargetType: "user",
			TargetID:   &userID,
			RequestID:  input.RequestID,
			Payload:    payload,
		}); err != nil {
			return err
		}
		updated = user
		return nil
	})
	return updated, err
}

func MountRoutes(r chi.Router, d Deps) {
	// SessionSafe:登录 admin(session)可直接写的用户账号运维/恢复类操作(危险者靠前端确认弹窗防误操作)。
	// 未挂此中间件的写端点默认 token-only(建/删用户、删 passkey、改分组=耦合计费档,均高危,继续只认令牌)。
	safe := adminsessionauth.AllowSessionWrite(adminsessionauth.SessionSafe)
	r.Get("/", newListHandler(d))
	r.Post("/", newCreateUserHandler(d))
	r.Delete("/{id}", newDeleteUserHandler(d))
	r.Get("/2fa-adoption-stats", newTwoFAStatsHandler(d))
	r.Get("/{id}", newGetHandler(d))
	r.With(safe).Post("/{id}/unlock", newUnlockHandler(d))
	r.With(safe).Post("/{id}/2fa/force-disable", newForceDisable2FAHandler(d))
	r.Delete("/{id}/passkeys", newResetPasskeyHandler(d))
	r.Put("/{id}/group", newSetUserGroupHandler(d))
	r.With(safe).Put("/{id}/remark", newSetUserRemarkHandler(d))
	r.With(safe).Put("/{id}/status", newSetUserStatusHandler(d))
	r.Get("/{id}/balance-history", newBalanceHistoryHandler(d))
	r.Get("/{id}/usage", newUserUsageHandler(d))
	r.With(safe).Delete("/{id}/account-bindings/{provider}", newUnlinkSocialIdentityHandler(d))
}

func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	MountRoutes(r, d)
	return r
}

func NewListHandler(d Deps) http.HandlerFunc {
	return newListHandler(d)
}

type userBody struct {
	ID        int64  `json:"id"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	UserGroup string `json:"user_group"`
	Remark    string `json:"remark"`
	Balance   string `json:"balance"`
	CreatedAt string `json:"created_at"`
}

type twoFAStatsBody struct {
	EnabledUsers int64   `json:"enabled_users"`
	TotalUsers   int64   `json:"total_users"`
	EnabledRate  float64 `json:"enabled_rate"`
}

type unlockUserBody struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
}

func newListHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := resolveTenant(w, r, d)
		if !ok {
			return
		}
		limit, offset, ok := pagination(w, r)
		if !ok {
			return
		}
		query := strings.TrimSpace(r.URL.Query().Get("q"))
		rows, err := d.Store.AdminListUsersForTenant(r.Context(), admindb.AdminListUsersForTenantParams{
			TenantID:   tenantID,
			Query:      query,
			PageLimit:  limit,
			PageOffset: offset,
		})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
				fmt.Sprintf("list users failed: %v", err))
			return
		}
		items := make([]userBody, 0, len(rows))
		for _, row := range rows {
			items = append(items, userBody{
				ID:        row.ID,
				Email:     row.Email,
				Role:      row.Role,
				Status:    row.Status,
				UserGroup: row.UserGroup,
				Remark:    row.Remark,
				Balance:   row.Balance,
				CreatedAt: timestamp(row.CreatedAt.Time),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items":  items,
			"limit":  limit,
			"offset": offset,
		})
	}
}

func newTwoFAStatsHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := resolveTenant(w, r, d)
		if !ok {
			return
		}
		row, err := d.Store.AdminGetTwoFAAdoptionStatsForTenant(r.Context(), tenantID)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
				fmt.Sprintf("get 2fa adoption stats failed: %v", err))
			return
		}
		rate := 0.0
		if row.TotalUserCount > 0 {
			rate = float64(row.EnabledCount) / float64(row.TotalUserCount)
		}
		writeJSON(w, http.StatusOK, twoFAStatsBody{
			EnabledUsers: row.EnabledCount,
			TotalUsers:   row.TotalUserCount,
			EnabledRate:  rate,
		})
	}
}

func newGetHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := resolveTenant(w, r, d)
		if !ok {
			return
		}
		userID, ok := pathID(w, r)
		if !ok {
			return
		}
		row, err := d.Store.AdminGetUserForTenant(r.Context(), admindb.AdminGetUserForTenantParams{
			TenantID: tenantID,
			UserID:   userID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "admin_user_not_found", "user not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
				fmt.Sprintf("get user failed: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, userBody{
			ID:        row.ID,
			Email:     row.Email,
			Role:      row.Role,
			Status:    row.Status,
			UserGroup: row.UserGroup,
			Remark:    row.Remark,
			Balance:   row.Balance,
			CreatedAt: timestamp(row.CreatedAt.Time),
		})
	}
}

func newUnlockHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveTenantIdentity(w, r, d)
		if !ok {
			return
		}
		userID, ok := pathID(w, r)
		if !ok {
			return
		}
		before, err := d.Store.AdminGetUserForTenant(r.Context(), admindb.AdminGetUserForTenantParams{
			TenantID: tenantID,
			UserID:   userID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "admin_user_not_found", "user not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
				fmt.Sprintf("get user failed: %v", err))
			return
		}
		auditInput := buildUnlockAuditInput(r, ident, before.Status)
		if d.UnlockAudit != nil {
			updated, err := d.UnlockAudit.UnlockUserWithAudit(r.Context(), tenantID, userID, auditInput)
			if err != nil {
				writeUnlockError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, unlockUserBody{
				ID:     updated.ID,
				Status: string(updated.Status),
			})
			return
		}
		if d.Unlocker == nil || d.Audit == nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_not_configured",
				"admin user mutation dependency unset")
			return
		}
		updated, err := d.Unlocker.UnlockUser(r.Context(), tenantID, userID)
		if err != nil {
			writeUnlockError(w, err)
			return
		}
		payload, err := marshalUnlockAuditPayload(auditInput.BeforeStatus, string(updated.Status))
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "audit_payload_failed", err.Error())
			return
		}
		audit := buildUnlockAuditParams(tenantID, userID, auditInput, payload)
		if _, err := d.Audit.InsertAdminAuditEvent(r.Context(), audit); err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
				fmt.Sprintf("write unlock audit failed: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, unlockUserBody{
			ID:     updated.ID,
			Status: string(updated.Status),
		})
	}
}

func newUnlinkSocialIdentityHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := resolveTenant(w, r, d)
		if !ok {
			return
		}
		if d.SocialLinks == nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_not_configured",
				"social link dependency unset")
			return
		}
		userID, ok := pathID(w, r)
		if !ok {
			return
		}
		provider := chi.URLParam(r, "provider")
		unlinked, err := d.SocialLinks.UnlinkSocialIdentity(r.Context(), tenantID, userID, provider)
		if err != nil {
			writeSocialLinkError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"unlinked": unlinked})
	}
}

func resolveTenant(w http.ResponseWriter, r *http.Request, d Deps) (int64, bool) {
	_, tenantID, ok := resolveTenantIdentity(w, r, d)
	return tenantID, ok
}

func resolveTenantIdentity(w http.ResponseWriter, r *http.Request, d Deps) (admin.AdminIdentity, int64, bool) {
	if d.Auth == nil || d.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "admin_users_not_configured",
			"admin users dependency unset")
		return admin.AdminIdentity{}, 0, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminAuthError(w, err)
		return admin.AdminIdentity{}, 0, false
	}
	switch ident.Role {
	case admin.RoleTenantOperator:
		if ident.ScopeTenantID <= 0 {
			writeError(w, http.StatusForbidden, "admin_tenant_scope_required",
				"tenant_operator scope_tenant_id required")
			return admin.AdminIdentity{}, 0, false
		}
		// tenant_operator 可省略 ?tenant_id,用自身 scope;若显式带,只能是自己的。
		if tenantID, ok := tenantFromQueryOrScope(w, r, ident); ok {
			return ident, tenantID, true
		}
		return admin.AdminIdentity{}, 0, false
	case admin.RolePlatformAdmin:
		// 单租户开箱即用(定位 2026-06-11):platform_admin 现可管理用户,镜像
		// provider_catalog 的 parseAdminCatalogTenant 模式——必须显式带
		// ?tenant_id,经 CanIssueForTenant 放行(单租户部署即默认租户 id)。
		// RBAC 语义不变:platform_admin 跨租户但须指名,越权由 CanIssueForTenant 挡。
		if tenantID, ok := tenantFromQueryOrScope(w, r, ident); ok {
			return ident, tenantID, true
		}
		return admin.AdminIdentity{}, 0, false
	default:
		writeError(w, http.StatusForbidden, "admin_forbidden_scope",
			"admin role required")
		return admin.AdminIdentity{}, 0, false
	}
}

func buildUnlockAuditInput(r *http.Request, ident admin.AdminIdentity, beforeStatus string) unlockAuditInput {
	actorRole := ident.Role
	if actorRole == "" {
		actorRole = admin.RoleTenantOperator
	}
	reqID := middleware.GetReqID(r.Context())
	var reqIDArg *string
	if reqID != "" {
		reqIDArg = &reqID
	}
	return unlockAuditInput{
		ActorID:      ident.AuditActor(),
		ActorRole:    actorRole,
		RequestID:    reqIDArg,
		BeforeStatus: beforeStatus,
	}
}

func marshalUnlockAuditPayload(beforeStatus, afterStatus string) ([]byte, error) {
	return json.Marshal(map[string]string{
		"status_before": beforeStatus,
		"status_after":  afterStatus,
	})
}

func buildUnlockAuditParams(tenantID, userID int64, input unlockAuditInput, payload []byte) admindb.InsertAdminAuditEventParams {
	return admindb.InsertAdminAuditEventParams{
		TenantID:   &tenantID,
		ActorID:    input.ActorID,
		ActorRole:  input.ActorRole,
		Action:     "unlock_user",
		TargetType: "user",
		TargetID:   &userID,
		RequestID:  input.RequestID,
		Payload:    payload,
	}
}

func writeUnlockError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, userauth.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_user_id", "user id must be a positive int64")
	case errors.Is(err, userauth.ErrUserNotFound), errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "admin_user_not_found", "user not found")
	default:
		writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
			fmt.Sprintf("unlock user failed: %v", err))
	}
}

func writeSocialLinkError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, userauth.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_account_binding", "account binding request is invalid")
	case errors.Is(err, userauth.ErrLastLoginMethod):
		writeError(w, http.StatusConflict, "last_login_method", "cannot remove the last login method")
	case errors.Is(err, userauth.ErrUserNotFound):
		writeError(w, http.StatusNotFound, "admin_user_not_found", "user not found")
	default:
		writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
			fmt.Sprintf("unlink social identity failed: %v", err))
	}
}

func pagination(w http.ResponseWriter, r *http.Request) (int32, int32, bool) {
	limit := defaultPageLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_limit",
				"limit must be a positive integer")
			return 0, 0, false
		}
		limit = int32(parsed)
		if limit > maxPageLimit {
			limit = maxPageLimit
		}
	}
	offset := int32(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "invalid_offset",
				"offset must be a non-negative integer")
			return 0, 0, false
		}
		offset = int32(parsed)
	}
	return limit, offset, true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(chi.URLParam(r, "id"))
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_user_id",
			"user id must be a positive int64")
		return 0, false
	}
	return id, true
}

func writeAdminAuthError(w http.ResponseWriter, err error) {
	if errors.Is(err, admin.ErrAdminBackend) {
		writeError(w, http.StatusServiceUnavailable, "admin_backend_error",
			"admin auth backend transient failure")
		return
	}
	if errors.Is(err, admin.ErrAdminForbidden) {
		writeError(w, http.StatusForbidden, "admin_forbidden_scope",
			"admin credential is not allowed for this tenant")
		return
	}
	writeError(w, http.StatusUnauthorized, "admin_unauthorized",
		"missing or invalid admin credential")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]map[string]string{
		"error": {
			"code":    code,
			"message": message,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func timestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// newForceDisable2FAHandler 让 tenant operator 强制清除被锁定用户的 TOTP 2FA
// (账号恢复),镜像 newUnlockHandler。按租户隔离 + 审计
// (action=force_disable_2fa)。AUTH-108b。
func newForceDisable2FAHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveTenantIdentity(w, r, d)
		if !ok {
			return
		}
		userID, ok := pathID(w, r)
		if !ok {
			return
		}
		if d.TwoFADisabler == nil || d.Audit == nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_not_configured", "admin 2fa mutation dependency unset")
			return
		}
		if _, err := d.Store.AdminGetUserForTenant(r.Context(), admindb.AdminGetUserForTenantParams{TenantID: tenantID, UserID: userID}); errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "admin_user_not_found", "user not found")
			return
		} else if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error", fmt.Sprintf("get user failed: %v", err))
			return
		}
		if err := d.TwoFADisabler.Disable(r.Context(), tenantID, userID); err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_2fa_disable_failed", fmt.Sprintf("force disable 2fa failed: %v", err))
			return
		}
		ai := buildUnlockAuditInput(r, ident, "")
		audit := admindb.InsertAdminAuditEventParams{TenantID: &tenantID, ActorID: ai.ActorID, ActorRole: ai.ActorRole, Action: "force_disable_2fa", TargetType: "user", TargetID: &userID, RequestID: ai.RequestID}
		if _, err := d.Audit.InsertAdminAuditEvent(r.Context(), audit); err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error", fmt.Sprintf("write 2fa audit failed: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": userID, "two_factor_enabled": false})
	}
}

// newResetPasskeyHandler 强制清除某用户的全部 passkey(admin 账号恢复),
// 镜像 newForceDisable2FAHandler。按租户隔离 + 审计
// (action=reset_passkey)。AUTH-098。
func newResetPasskeyHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveTenantIdentity(w, r, d)
		if !ok {
			return
		}
		userID, ok := pathID(w, r)
		if !ok {
			return
		}
		if d.PasskeyResetter == nil || d.Audit == nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_not_configured", "admin passkey mutation dependency unset")
			return
		}
		if _, err := d.Store.AdminGetUserForTenant(r.Context(), admindb.AdminGetUserForTenantParams{TenantID: tenantID, UserID: userID}); errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "admin_user_not_found", "user not found")
			return
		} else if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error", fmt.Sprintf("get user failed: %v", err))
			return
		}
		cleared, err := d.PasskeyResetter.AdminClearCredentials(r.Context(), tenantID, userID)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_passkey_reset_failed", fmt.Sprintf("reset passkeys failed: %v", err))
			return
		}
		ai := buildUnlockAuditInput(r, ident, "")
		audit := admindb.InsertAdminAuditEventParams{TenantID: &tenantID, ActorID: ai.ActorID, ActorRole: ai.ActorRole, Action: "reset_passkey", TargetType: "user", TargetID: &userID, RequestID: ai.RequestID}
		if _, err := d.Audit.InsertAdminAuditEvent(r.Context(), audit); err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error", fmt.Sprintf("write passkey audit failed: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": userID, "cleared": cleared})
	}
}

type setUserGroupRequest struct {
	Group string `json:"group"`
}

// postgresUserGroupStore 为某租户用户设置 users.user_group(路由权益)。
type postgresUserGroupStore struct {
	pool *pgxpool.Pool
}

// NewPostgresUserGroupStore 接线 admin 用户分组 setter。
func NewPostgresUserGroupStore(pool *pgxpool.Pool) userGroupSetter {
	if pool == nil {
		return nil
	}
	return postgresUserGroupStore{pool: pool}
}

func (s postgresUserGroupStore) SetUserGroupForTenant(ctx context.Context, tenantID, userID int64, group string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET user_group=$3 WHERE tenant_id=$1 AND id=$2`, tenantID, userID, group)
	return err
}

// newSetUserGroupHandler 让 tenant operator 设置某用户的路由分组
// (users.user_group),镜像 newForceDisable2FAHandler。按租户隔离 + 审计
// (action=set_user_group)。AUTH-031。增量式 admin 管理;保留默认行为。
func newSetUserGroupHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveTenantIdentity(w, r, d)
		if !ok {
			return
		}
		userID, ok := pathID(w, r)
		if !ok {
			return
		}
		var req setUserGroupRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		group := strings.TrimSpace(req.Group)
		if group == "" || len(group) > 64 {
			writeError(w, http.StatusBadRequest, "invalid_group", "group must be 1..64 chars")
			return
		}
		if d.UserGroupSetter == nil || d.Audit == nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_not_configured", "admin user-group dependency unset")
			return
		}
		if _, err := d.Store.AdminGetUserForTenant(r.Context(), admindb.AdminGetUserForTenantParams{TenantID: tenantID, UserID: userID}); errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "admin_user_not_found", "user not found")
			return
		} else if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error", fmt.Sprintf("get user failed: %v", err))
			return
		}
		if err := d.UserGroupSetter.SetUserGroupForTenant(r.Context(), tenantID, userID, group); err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_user_group_failed", fmt.Sprintf("set user group failed: %v", err))
			return
		}
		ai := buildUnlockAuditInput(r, ident, "")
		audit := admindb.InsertAdminAuditEventParams{
			TenantID: &tenantID, ActorID: ai.ActorID, ActorRole: ai.ActorRole,
			Action: "set_user_group", TargetType: "user", TargetID: &userID, RequestID: ai.RequestID,
		}
		if _, err := d.Audit.InsertAdminAuditEvent(r.Context(), audit); err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error", fmt.Sprintf("write group audit failed: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": userID, "user_group": group})
	}
}

type setUserRemarkRequest struct {
	Remark string `json:"remark"`
}

type postgresUserRemarkStore struct {
	pool *pgxpool.Pool
}

// NewPostgresUserRemarkStore 接线 admin 用户备注 setter。
func NewPostgresUserRemarkStore(pool *pgxpool.Pool) userRemarkSetter {
	if pool == nil {
		return nil
	}
	return postgresUserRemarkStore{pool: pool}
}

func (s postgresUserRemarkStore) SetUserRemarkForTenant(ctx context.Context, tenantID, userID int64, remark string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET remark=$3 WHERE tenant_id=$1 AND id=$2`, tenantID, userID, remark)
	return err
}

// newSetUserRemarkHandler 让 tenant operator 为某用户设置自由文本 admin 备注
// (users.remark),镜像 newSetUserGroupHandler。按租户隔离 + 审计
// (action=set_user_remark)。AUTH-030。增量式 admin 管理;保留默认行为。
func newSetUserRemarkHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveTenantIdentity(w, r, d)
		if !ok {
			return
		}
		userID, ok := pathID(w, r)
		if !ok {
			return
		}
		var req setUserRemarkRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		remark := strings.TrimSpace(req.Remark)
		if len(remark) > 1024 {
			writeError(w, http.StatusBadRequest, "invalid_remark", "remark must be <= 1024 chars")
			return
		}
		if d.UserRemarkSetter == nil || d.Audit == nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_not_configured", "admin user-remark dependency unset")
			return
		}
		if _, err := d.Store.AdminGetUserForTenant(r.Context(), admindb.AdminGetUserForTenantParams{TenantID: tenantID, UserID: userID}); errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "admin_user_not_found", "user not found")
			return
		} else if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error", fmt.Sprintf("get user failed: %v", err))
			return
		}
		if err := d.UserRemarkSetter.SetUserRemarkForTenant(r.Context(), tenantID, userID, remark); err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_user_remark_failed", fmt.Sprintf("set user remark failed: %v", err))
			return
		}
		ai := buildUnlockAuditInput(r, ident, "")
		// payload 列 NOT NULL(默认 '{}');必须显式给非 NULL payload,否则 INSERT 撞 23502 → 503 且审计丢失。
		// 记录改后的备注长度(不落原文,备注可能含敏感信息;长度足够审计追踪)。
		remarkPayload, err := json.Marshal(map[string]any{"remark_length": len([]rune(remark))})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "audit_payload_failed", err.Error())
			return
		}
		audit := admindb.InsertAdminAuditEventParams{
			TenantID: &tenantID, ActorID: ai.ActorID, ActorRole: ai.ActorRole,
			Action: "set_user_remark", TargetType: "user", TargetID: &userID, RequestID: ai.RequestID,
			Payload: remarkPayload,
		}
		if _, err := d.Audit.InsertAdminAuditEvent(r.Context(), audit); err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error", fmt.Sprintf("write remark audit failed: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": userID, "remark": remark})
	}
}
