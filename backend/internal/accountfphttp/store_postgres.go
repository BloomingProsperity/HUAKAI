package accountfphttp

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

var errStoreNotConfigured = errors.New("fingerprint binding store is not configured")

type postgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore 让指纹绑定和操作日志在同一数据库事务内提交。
func NewPostgresStore(pool *pgxpool.Pool) Store {
	return postgresStore{pool: pool}
}

func (s postgresStore) UpdateFingerprintProfileWithAudit(
	ctx context.Context,
	arg admindb.UpdateProviderAccountFingerprintProfileParams,
	audit admindb.InsertAdminAuditEventParams,
) error {
	if s.pool == nil {
		return errStoreNotConfigured
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var accountID int64
		if err := tx.QueryRow(ctx, `
SELECT id
FROM provider_accounts
WHERE tenant_id = $1
  AND id = $2
  AND deleted_at IS NULL
FOR UPDATE`, arg.TenantID, arg.ID).Scan(&accountID); err != nil {
			return err
		}
		q := admindb.New(tx)
		if err := q.UpdateProviderAccountFingerprintProfile(ctx, arg); err != nil {
			return err
		}
		audit.TargetID = &accountID
		_, err := q.InsertAdminAuditEvent(ctx, audit)
		return err
	})
}
