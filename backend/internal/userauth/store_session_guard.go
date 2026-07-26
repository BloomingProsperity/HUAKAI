package userauth

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

// AssertActiveAuthSession 必须在调用方业务事务内执行。它与管理员安全恢复共用用户锁，
// 并同时核对会话族和用户当前认证版本，防止已经过中间件的旧请求在恢复完成后继续落库。
func (s *PostgresStore) AssertActiveAuthSession(
	ctx context.Context,
	tenantID, userID int64,
	familyID string,
	authVersion int,
) error {
	if s == nil || s.db == nil {
		return ErrStoreNotConfigured
	}
	tx, ok := s.db.(pgx.Tx)
	if !ok {
		return ErrStoreNotConfigured
	}
	if tenantID <= 0 || userID <= 0 || strings.TrimSpace(familyID) == "" || authVersion <= 0 {
		return ErrInvalidInput
	}
	if err := usersession.LockUserSessionsInTransaction(ctx, tx, tenantID, userID); err != nil {
		return err
	}
	var familyVersion, userVersion int
	err := tx.QueryRow(ctx, `
SELECT sf.auth_version, u.password_version
FROM session_families sf
INNER JOIN users u
  ON u.tenant_id = sf.tenant_id
 AND u.id = sf.user_id
WHERE sf.tenant_id = $1
  AND sf.user_id = $2
  AND sf.id = $3::uuid
  AND sf.status IN ('active', 'suspicious')
  AND u.status = 'active'
  AND u.deleted_at IS NULL
FOR UPDATE OF sf, u`, tenantID, userID, familyID).Scan(&familyVersion, &userVersion)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAuthenticationStale
		}
		return err
	}
	if familyVersion != authVersion || userVersion != authVersion {
		return ErrAuthenticationStale
	}
	return nil
}
