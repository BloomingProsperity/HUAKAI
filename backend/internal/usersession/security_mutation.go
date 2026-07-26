package usersession

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// LockUserSessionsInTransaction 获取用户级会话事务锁。
// 所有同时改变用户安全状态与会话状态的跨包事务必须先取这把锁，再锁业务行。
func LockUserSessionsInTransaction(ctx context.Context, tx pgx.Tx, tenantID, userID int64) error {
	if tx == nil || tenantID <= 0 || userID <= 0 {
		return ErrInvalidInput
	}
	lockKey := strconv.FormatInt(tenantID, 10) + ":" + strconv.FormatInt(userID, 10)
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('usersession_device:' || $1, 0))`, lockKey)
	return err
}

// RevokeOtherFamiliesInTransaction 在调用方事务内保留当前会话族，并撤销同一用户的其他会话族及令牌。
// 它与会话创建共用用户级事务锁，防止安全状态变更期间并发签发一条漏网会话。
func RevokeOtherFamiliesInTransaction(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, userID int64,
	currentFamilyID, reason string,
	now time.Time,
) (int64, error) {
	currentFamilyID = strings.TrimSpace(currentFamilyID)
	if tenantID <= 0 || userID <= 0 || currentFamilyID == "" {
		return 0, ErrInvalidInput
	}
	if _, err := uuid.Parse(currentFamilyID); err != nil {
		return 0, ErrFamilyNotFound
	}
	if err := LockUserSessionsInTransaction(ctx, tx, tenantID, userID); err != nil {
		return 0, err
	}
	var currentExists bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM session_families
    WHERE tenant_id = $1
      AND user_id = $2
      AND id = $3::uuid
      AND status IN ('active', 'suspicious')
)`, tenantID, userID, currentFamilyID).Scan(&currentExists); err != nil {
		return 0, err
	}
	if !currentExists {
		return 0, ErrFamilyNotFound
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "user_requested"
	}
	changedAt := now.UTC()
	tag, err := tx.Exec(ctx, `
UPDATE session_families
SET status = 'revoked',
    revoked_at = $5,
    revoked_reason = $4,
    last_active_at = $5
WHERE tenant_id = $1
  AND user_id = $2
  AND id <> $3::uuid
  AND status IN ('active', 'suspicious')`,
		tenantID, userID, currentFamilyID, reason, changedAt)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
UPDATE refresh_tokens rt
SET status = 'revoked',
    consumed_at = COALESCE(rt.consumed_at, $4)
FROM session_families sf
WHERE rt.tenant_id = $1
  AND rt.family_id = sf.id
  AND sf.tenant_id = $1
  AND sf.user_id = $2
  AND sf.id <> $3::uuid
  AND rt.status = 'active'`,
		tenantID, userID, currentFamilyID, changedAt); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
UPDATE session_tokens st
SET revoked_at = COALESCE(st.revoked_at, $4)
FROM session_families sf
WHERE st.tenant_id = $1
  AND st.family_id = sf.id
  AND sf.tenant_id = $1
  AND sf.user_id = $2
  AND sf.id <> $3::uuid
  AND st.revoked_at IS NULL`,
		tenantID, userID, currentFamilyID, changedAt); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// RevokeUserInTransaction 在调用方事务内撤销用户的全部会话族及令牌。
func RevokeUserInTransaction(
	ctx context.Context,
	tx pgx.Tx,
	tenantID, userID int64,
	reason string,
	now time.Time,
) (int64, error) {
	if tenantID <= 0 || userID <= 0 {
		return 0, ErrInvalidInput
	}
	if err := LockUserSessionsInTransaction(ctx, tx, tenantID, userID); err != nil {
		return 0, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "user_requested"
	}
	changedAt := now.UTC()
	tag, err := tx.Exec(ctx, `
UPDATE session_families
SET status = 'revoked',
    revoked_at = $4,
    revoked_reason = $3,
    last_active_at = $4
WHERE tenant_id = $1
  AND user_id = $2
  AND status IN ('active', 'suspicious')`,
		tenantID, userID, reason, changedAt)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
UPDATE refresh_tokens rt
SET status = 'revoked',
    consumed_at = COALESCE(rt.consumed_at, $3)
FROM session_families sf
WHERE rt.tenant_id = $1
  AND rt.family_id = sf.id
  AND sf.tenant_id = $1
  AND sf.user_id = $2
  AND rt.status = 'active'`,
		tenantID, userID, changedAt); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
UPDATE session_tokens st
SET revoked_at = COALESCE(st.revoked_at, $3)
FROM session_families sf
WHERE st.tenant_id = $1
  AND st.family_id = sf.id
  AND sf.tenant_id = $1
  AND sf.user_id = $2
  AND st.revoked_at IS NULL`,
		tenantID, userID, changedAt); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
