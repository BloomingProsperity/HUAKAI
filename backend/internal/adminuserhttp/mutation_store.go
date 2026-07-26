package adminuserhttp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

var errUserMutationStoreNotConfigured = errors.New("admin user mutation store is not configured")
var errUserStatusTransitionConflict = errors.New("admin user status transition conflicts with recovery state")

type userMutationService interface {
	UnlinkSocialIdentityWithAudit(context.Context, int64, int64, string, unlockAuditInput) (bool, int64, error)
	ForceDisableTwoFAWithAudit(context.Context, int64, int64, unlockAuditInput) (int64, error)
	ResetPasskeysWithAudit(context.Context, int64, int64, unlockAuditInput) (int, int64, error)
	SetUserGroupWithAudit(context.Context, int64, int64, string, unlockAuditInput) error
	SetUserRemarkWithAudit(context.Context, int64, int64, string, unlockAuditInput) error
	SetUserStatusWithAudit(context.Context, int64, int64, string, string, unlockAuditInput) (int64, error)
	SoftDeleteUserWithAudit(context.Context, int64, int64, unlockAuditInput) (int64, error)
}

type postgresUserMutationStore struct {
	pool *pgxpool.Pool
}

// NewPostgresUserMutationStore 统一终端用户状态写入和操作日志的事务边界。
func NewPostgresUserMutationStore(pool *pgxpool.Pool) userMutationService {
	if pool == nil {
		return nil
	}
	return postgresUserMutationStore{pool: pool}
}

type lockedFinalUser struct {
	Status          string
	UserGroup       string
	Remark          string
	PasswordVersion int
}

func (s postgresUserMutationStore) UnlinkSocialIdentityWithAudit(
	ctx context.Context,
	tenantID, userID int64,
	provider string,
	audit unlockAuditInput,
) (bool, int64, error) {
	var unlinked bool
	var sessionsRevoked int64
	err := s.withLockedFinalUserSecurity(ctx, tenantID, userID, audit, "unlink_social_identity", nil,
		func(tx pgx.Tx, _ lockedFinalUser) (map[string]any, error) {
			service := userauth.NewService(userauth.NewPostgresStore(tx))
			var err error
			unlinked, err = service.UnlinkSocialIdentity(ctx, tenantID, userID, provider)
			if err != nil {
				return nil, err
			}
			sessionsRevoked, err = revokeAdminUserSessions(ctx, tx, tenantID, userID, "admin_social_identity_unlinked")
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"provider":         provider,
				"unlinked":         unlinked,
				"sessions_revoked": sessionsRevoked,
			}, nil
		})
	return unlinked, sessionsRevoked, err
}

func (s postgresUserMutationStore) ForceDisableTwoFAWithAudit(
	ctx context.Context,
	tenantID, userID int64,
	audit unlockAuditInput,
) (int64, error) {
	var sessionsRevoked int64
	err := s.withLockedFinalUserSecurity(ctx, tenantID, userID, audit, "force_disable_2fa", nil,
		func(tx pgx.Tx, _ lockedFinalUser) (map[string]any, error) {
			backupTag, err := tx.Exec(ctx, `
DELETE FROM two_factor_backup_codes
WHERE tenant_id=$1
  AND user_id=$2`, tenantID, userID)
			if err != nil {
				return nil, err
			}
			settingTag, err := tx.Exec(ctx, `
DELETE FROM two_factor_settings
WHERE tenant_id=$1
  AND user_id=$2`, tenantID, userID)
			if err != nil {
				return nil, err
			}
			sessionsRevoked, err = revokeAdminUserSessions(ctx, tx, tenantID, userID, "admin_two_factor_disabled")
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"two_factor_enabled":   false,
				"enrollment_removed":   settingTag.RowsAffected() > 0,
				"backup_codes_cleared": backupTag.RowsAffected(),
				"sessions_revoked":     sessionsRevoked,
			}, nil
		})
	return sessionsRevoked, err
}

func (s postgresUserMutationStore) ResetPasskeysWithAudit(
	ctx context.Context,
	tenantID, userID int64,
	audit unlockAuditInput,
) (int, int64, error) {
	cleared := 0
	var sessionsRevoked int64
	err := s.withLockedFinalUserSecurity(ctx, tenantID, userID, audit, "reset_passkey", nil,
		func(tx pgx.Tx, _ lockedFinalUser) (map[string]any, error) {
			tag, err := tx.Exec(ctx, `
DELETE FROM passkey_credentials
WHERE tenant_id=$1 AND user_id=$2`, tenantID, userID)
			if err != nil {
				return nil, err
			}
			cleared = int(tag.RowsAffected())
			sessionsRevoked, err = revokeAdminUserSessions(ctx, tx, tenantID, userID, "admin_passkeys_reset")
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"cleared":          cleared,
				"sessions_revoked": sessionsRevoked,
			}, nil
		})
	return cleared, sessionsRevoked, err
}

func (s postgresUserMutationStore) SetUserGroupWithAudit(
	ctx context.Context,
	tenantID, userID int64,
	group string,
	audit unlockAuditInput,
) error {
	return s.withLockedFinalUser(ctx, tenantID, userID, audit, "set_user_group", nil,
		func(tx pgx.Tx, before lockedFinalUser) (map[string]any, error) {
			if _, err := tx.Exec(ctx, `
UPDATE users
SET user_group=$3, updated_at=now()
WHERE tenant_id=$1 AND id=$2`, tenantID, userID, group); err != nil {
				return nil, err
			}
			return map[string]any{
				"group_before": before.UserGroup,
				"group_after":  group,
			}, nil
		})
}

func (s postgresUserMutationStore) SetUserRemarkWithAudit(
	ctx context.Context,
	tenantID, userID int64,
	remark string,
	audit unlockAuditInput,
) error {
	return s.withLockedFinalUser(ctx, tenantID, userID, audit, "set_user_remark", nil,
		func(tx pgx.Tx, before lockedFinalUser) (map[string]any, error) {
			if _, err := tx.Exec(ctx, `
UPDATE users
SET remark=$3, updated_at=now()
WHERE tenant_id=$1 AND id=$2`, tenantID, userID, remark); err != nil {
				return nil, err
			}
			return map[string]any{
				"remark_length_before": utf8.RuneCountInString(before.Remark),
				"remark_length_after":  utf8.RuneCountInString(remark),
			}, nil
		})
}

func (s postgresUserMutationStore) SetUserStatusWithAudit(
	ctx context.Context,
	tenantID, userID int64,
	status, reason string,
	audit unlockAuditInput,
) (int64, error) {
	var sessionsRevoked int64
	run := s.withLockedFinalUser
	if status == "disabled" {
		run = s.withLockedFinalUserSecurity
	}
	err := run(ctx, tenantID, userID, audit, "set_user_status", optionalReason(reason),
		func(tx pgx.Tx, before lockedFinalUser) (map[string]any, error) {
			if before.Status != "active" && before.Status != "disabled" {
				return nil, errUserStatusTransitionConflict
			}
			if _, err := tx.Exec(ctx, `
UPDATE users
SET status=$3, updated_at=now()
WHERE tenant_id=$1 AND id=$2`, tenantID, userID, status); err != nil {
				return nil, err
			}
			if status == "disabled" {
				var err error
				sessionsRevoked, err = revokeAdminUserSessions(ctx, tx, tenantID, userID, "admin_user_disabled")
				if err != nil {
					return nil, err
				}
			}
			return map[string]any{
				"status_before":    before.Status,
				"status_after":     status,
				"sessions_revoked": sessionsRevoked,
			}, nil
		})
	return sessionsRevoked, err
}

func (s postgresUserMutationStore) SoftDeleteUserWithAudit(
	ctx context.Context,
	tenantID, userID int64,
	audit unlockAuditInput,
) (int64, error) {
	var sessionsRevoked int64
	err := s.withLockedFinalUserSecurity(ctx, tenantID, userID, audit, "delete_user", nil,
		func(tx pgx.Tx, before lockedFinalUser) (map[string]any, error) {
			if _, err := tx.Exec(ctx, `
UPDATE users
SET deleted_at=now(), status='deleted', updated_at=now()
WHERE tenant_id=$1 AND id=$2`, tenantID, userID); err != nil {
				return nil, err
			}
			var err error
			sessionsRevoked, err = revokeAdminUserSessions(ctx, tx, tenantID, userID, "admin_user_deleted")
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"prior_status":     before.Status,
				"sessions_revoked": sessionsRevoked,
			}, nil
		})
	return sessionsRevoked, err
}

func (s postgresUserMutationStore) withLockedFinalUser(
	ctx context.Context,
	tenantID, userID int64,
	audit unlockAuditInput,
	action string,
	reason *string,
	mutate func(pgx.Tx, lockedFinalUser) (map[string]any, error),
) error {
	return s.withLockedFinalUserMode(ctx, tenantID, userID, audit, action, reason, false, mutate)
}

func (s postgresUserMutationStore) withLockedFinalUserSecurity(
	ctx context.Context,
	tenantID, userID int64,
	audit unlockAuditInput,
	action string,
	reason *string,
	mutate func(pgx.Tx, lockedFinalUser) (map[string]any, error),
) error {
	return s.withLockedFinalUserMode(ctx, tenantID, userID, audit, action, reason, true, mutate)
}

func (s postgresUserMutationStore) withLockedFinalUserMode(
	ctx context.Context,
	tenantID, userID int64,
	audit unlockAuditInput,
	action string,
	reason *string,
	lockSessions bool,
	mutate func(pgx.Tx, lockedFinalUser) (map[string]any, error),
) error {
	if s.pool == nil {
		return errUserMutationStoreNotConfigured
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if lockSessions {
			if err := usersession.LockUserSessionsInTransaction(ctx, tx, tenantID, userID); err != nil {
				return err
			}
		}
		before, err := lockFinalUser(ctx, tx, tenantID, userID)
		if err != nil {
			return err
		}
		payloadValue, err := mutate(tx, before)
		if err != nil {
			return err
		}
		if lockSessions {
			var nextVersion int
			if err := tx.QueryRow(ctx, `
UPDATE users
SET password_version = password_version + 1,
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
RETURNING password_version`, tenantID, userID).Scan(&nextVersion); err != nil {
				return err
			}
			if payloadValue == nil {
				payloadValue = map[string]any{}
			}
			payloadValue["auth_version_before"] = before.PasswordVersion
			payloadValue["auth_version_after"] = nextVersion
		}
		payload, err := json.Marshal(payloadValue)
		if err != nil {
			return err
		}
		if len(payload) == 0 {
			payload = []byte(`{}`)
		}
		_, err = admindb.New(tx).InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
			TenantID:   &tenantID,
			ActorID:    audit.ActorID,
			ActorRole:  audit.ActorRole,
			Action:     action,
			TargetType: "user",
			TargetID:   &userID,
			RequestID:  audit.RequestID,
			Reason:     reason,
			Payload:    payload,
		})
		return err
	})
}

func revokeAdminUserSessions(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, userID int64,
	reason string,
) (int64, error) {
	return usersession.RevokeUserInTransaction(ctx, tx, tenantID, userID, reason, time.Now().UTC())
}

func lockFinalUser(ctx context.Context, tx pgx.Tx, tenantID, userID int64) (lockedFinalUser, error) {
	var out lockedFinalUser
	err := tx.QueryRow(ctx, `
SELECT status, COALESCE(user_group, ''), COALESCE(remark, ''), password_version
FROM users
WHERE tenant_id=$1
  AND id=$2
  AND principal_kind='human'
  AND role='user'
  AND deleted_at IS NULL
FOR UPDATE`, tenantID, userID).Scan(&out.Status, &out.UserGroup, &out.Remark, &out.PasswordVersion)
	return out, err
}

func optionalReason(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
