package adminpoolhttp

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

var errProviderAccountMutationTxUnavailable = errors.New("provider account mutation transaction pool unset")

type adminPoolAccountStoreAdapter struct {
	base AdminPoolAccountDataStore
	pool *pgxpool.Pool
}

// NewAdminPoolAccountStoreAdapter 为账号普通写操作提供同事务日志。
func NewAdminPoolAccountStoreAdapter(base AdminPoolAccountDataStore, pool *pgxpool.Pool) AdminPoolAccountStore {
	return adminPoolAccountStoreAdapter{base: base, pool: pool}
}

func (s adminPoolAccountStoreAdapter) GetProviderProtocolForAccountCreate(
	ctx context.Context,
	arg admindb.GetProviderProtocolForAccountCreateParams,
) (string, error) {
	if s.base == nil {
		return "", errProviderAccountMutationTxUnavailable
	}
	return s.base.GetProviderProtocolForAccountCreate(ctx, arg)
}

func (s adminPoolAccountStoreAdapter) InsertProviderAccount(
	ctx context.Context,
	arg admindb.InsertProviderAccountParams,
) (int64, error) {
	if s.base == nil {
		return 0, errProviderAccountMutationTxUnavailable
	}
	return s.base.InsertProviderAccount(ctx, arg)
}

func (s adminPoolAccountStoreAdapter) ListAdminProviderAccounts(
	ctx context.Context,
	arg admindb.ListAdminProviderAccountsParams,
) ([]admindb.AdminProviderAccountRow, error) {
	if s.base == nil {
		return nil, errProviderAccountMutationTxUnavailable
	}
	return s.base.ListAdminProviderAccounts(ctx, arg)
}

func (s adminPoolAccountStoreAdapter) GetAdminProviderAccount(
	ctx context.Context,
	arg admindb.GetAdminProviderAccountParams,
) (admindb.AdminProviderAccountRow, error) {
	if s.base == nil {
		return admindb.AdminProviderAccountRow{}, errProviderAccountMutationTxUnavailable
	}
	return s.base.GetAdminProviderAccount(ctx, arg)
}

func (s adminPoolAccountStoreAdapter) InsertAdminAuditEvent(
	ctx context.Context,
	arg admindb.InsertAdminAuditEventParams,
) (admindb.InsertAdminAuditEventRow, error) {
	if s.base == nil {
		return admindb.InsertAdminAuditEventRow{}, errProviderAccountMutationTxUnavailable
	}
	return s.base.InsertAdminAuditEvent(ctx, arg)
}

func (s adminPoolAccountStoreAdapter) UpdateAdminProviderAccountWithAudit(
	ctx context.Context,
	arg admindb.UpdateAdminProviderAccountParams,
	audit admindb.InsertAdminAuditEventParams,
) (admindb.AdminProviderAccountRow, error) {
	if s.pool == nil {
		return admindb.AdminProviderAccountRow{}, errProviderAccountMutationTxUnavailable
	}
	var updated admindb.AdminProviderAccountRow
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := admindb.New(tx)
		row, err := q.UpdateAdminProviderAccount(ctx, arg)
		if err != nil {
			return err
		}
		audit.TargetID = &row.ID
		if _, err := q.InsertAdminAuditEvent(ctx, audit); err != nil {
			return err
		}
		updated = row
		return nil
	})
	return updated, err
}

func (s adminPoolAccountStoreAdapter) UpdateProviderAccountEnabledWithAudit(
	ctx context.Context,
	arg admindb.UpdateProviderAccountEnabledParams,
	audit admindb.InsertAdminAuditEventParams,
) error {
	if s.pool == nil {
		return errProviderAccountMutationTxUnavailable
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockProviderAccount(ctx, tx, arg.TenantID, arg.ID); err != nil {
			return err
		}
		q := admindb.New(tx)
		if err := q.UpdateProviderAccountEnabled(ctx, arg); err != nil {
			return err
		}
		audit.TargetID = &arg.ID
		_, err := q.InsertAdminAuditEvent(ctx, audit)
		return err
	})
}

func (s adminPoolAccountStoreAdapter) SoftDeleteProviderAccountWithAudit(
	ctx context.Context,
	arg admindb.SoftDeleteProviderAccountParams,
	audit admindb.InsertAdminAuditEventParams,
) error {
	if s.pool == nil {
		return errProviderAccountMutationTxUnavailable
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockProviderAccount(ctx, tx, arg.TenantID, arg.ID); err != nil {
			return err
		}
		q := admindb.New(tx)
		if err := q.SoftDeleteProviderAccount(ctx, arg); err != nil {
			return err
		}
		audit.TargetID = &arg.ID
		_, err := q.InsertAdminAuditEvent(ctx, audit)
		return err
	})
}

func lockProviderAccount(ctx context.Context, tx pgx.Tx, tenantID, accountID int64) error {
	var id int64
	return tx.QueryRow(ctx, `
SELECT id
FROM provider_accounts
WHERE tenant_id = $1
  AND id = $2
  AND deleted_at IS NULL
FOR UPDATE`, tenantID, accountID).Scan(&id)
}
