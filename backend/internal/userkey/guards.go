package userkey

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// requireActiveFinalUser 在生成明文与 bcrypt 之前确认会话主体仍是有效终端用户。
// 最终写入仍保留同样的 WHERE 条件，用于封住预检到提交之间的竞态。
func (s *Service) requireActiveFinalUser(ctx context.Context, tenantID, userID int64) error {
	var allowed bool
	if err := s.pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM users u
    JOIN tenants t ON t.id=u.tenant_id
    WHERE u.tenant_id=$1
      AND u.id=$2
      AND u.principal_kind='human'
      AND u.role='user'
      AND u.status='active'
      AND u.deleted_at IS NULL
      AND t.status='active'
      AND t.deleted_at IS NULL
)`, tenantID, userID).Scan(&allowed); err != nil {
		return fmt.Errorf("%w: validate final user: %v", ErrBackend, err)
	}
	if !allowed {
		return ErrNotFound
	}
	return nil
}

func lockPatchCurrentStatus(ctx context.Context, tx pgx.Tx, req PatchRequest) (string, error) {
	var status string
	if err := tx.QueryRow(ctx,
		`SELECT k.status
		   FROM api_keys k
		   JOIN tenants t ON t.id=k.tenant_id AND t.status='active' AND t.deleted_at IS NULL
		   JOIN users u ON u.id=k.user_id AND u.tenant_id=k.tenant_id
		               AND u.principal_kind='human' AND u.role='user'
		               AND u.status='active' AND u.deleted_at IS NULL
		  WHERE k.id=$1 AND k.tenant_id=$2 AND k.user_id=$3
		    AND k.purpose='user' AND k.deleted_at IS NULL
		  FOR UPDATE OF k
		  FOR SHARE OF t, u`,
		req.APIKeyID, req.TenantID, req.UserID,
	).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("%w: lock patch target: %v", ErrBackend, err)
	}
	return status, nil
}
