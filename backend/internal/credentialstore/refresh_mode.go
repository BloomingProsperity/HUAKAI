package credentialstore

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// inspectRefreshModeQuery 只读当前账号凭据的 vendor/auth_mode,不解密、不要求
// refresh_before_at。运营"立刻刷新"用它判断静态凭据,不得走到期扫描谓词。
const inspectRefreshModeQuery = `
SELECT ac.vendor, ac.auth_mode
FROM account_credentials ac
JOIN provider_accounts pa
  ON pa.id = ac.provider_account_id
 AND pa.tenant_id = ac.tenant_id
WHERE ac.provider_account_id = $1
  AND ac.tenant_id = $2
  AND ac.deleted_at IS NULL
  AND pa.deleted_at IS NULL
  AND ac.state IN ('active', 'refreshing', 'refreshing_with_grace', 'temp_unschedulable', 'needs_rotation', 'operator_attention')
ORDER BY ac.credential_version DESC, ac.id DESC
LIMIT 1`

// InspectRefreshMode 返回账号当前活跃凭据的厂商与认证模式。无行或他租户 →
// ErrCredentialNotFound。调用方不得把该错误当成基础设施故障。
func (s *Store) InspectRefreshMode(ctx context.Context, tenantID, providerAccountID int64) (string, string, error) {
	if s == nil || s.db == nil {
		return "", "", errors.New("credentialstore: db is nil")
	}
	if tenantID <= 0 || providerAccountID <= 0 {
		return "", "", errors.New("credentialstore: inspect refresh mode input invalid")
	}
	var vendor, authMode string
	err := s.db.QueryRow(ctx, inspectRefreshModeQuery, providerAccountID, tenantID).Scan(&vendor, &authMode)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrCredentialNotFound
	}
	if err != nil {
		return "", "", err
	}
	return vendor, authMode, nil
}
