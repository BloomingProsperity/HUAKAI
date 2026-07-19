package provideraccountrecovery

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) ClearRateLimitWithAudit(ctx context.Context, mutation AccountMutation) (admindb.AdminProviderAccountRow, error) {
	if s == nil || s.pool == nil {
		return admindb.AdminProviderAccountRow{}, errors.New("provider account recovery postgres store is not configured")
	}
	var out admindb.AdminProviderAccountRow
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := admindb.New(tx)
		account, err := q.ClearProviderAccountRateLimit(ctx, mutation.Clear)
		if err != nil {
			return err
		}
		mutation.Audit.TargetID = &account.ID
		if _, err := q.InsertAdminAuditEvent(ctx, mutation.Audit); err != nil {
			return err
		}
		out = account
		return nil
	})
	if err != nil {
		return admindb.AdminProviderAccountRow{}, err
	}
	return out, nil
}

func (s *PostgresStore) RecoverAccountStateWithAudit(ctx context.Context, mutation AccountRecoverMutation) (admindb.AdminProviderAccountRow, error) {
	if s == nil || s.pool == nil {
		return admindb.AdminProviderAccountRow{}, errors.New("provider account recovery postgres store is not configured")
	}
	var out admindb.AdminProviderAccountRow
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := admindb.New(tx)
		account, err := q.RecoverProviderAccountState(ctx, mutation.Recover)
		if err != nil {
			return err
		}
		mutation.Audit.TargetID = &account.ID
		if _, err := q.InsertAdminAuditEvent(ctx, mutation.Audit); err != nil {
			return err
		}
		out = account
		return nil
	})
	if err != nil {
		return admindb.AdminProviderAccountRow{}, err
	}
	return out, nil
}
