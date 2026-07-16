package admin

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrProviderAccountBulkTransactionUnavailable = errors.New("admin: provider account bulk transaction unavailable")

type UpdateAdminProviderAccountWithAuditParams struct {
	Update UpdateAdminProviderAccountParams
	Audit  InsertAdminAuditEventParams
}

type providerAccountBulkTxBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

// UpdateAdminProviderAccountWithAudit 保证单个账号更新与对应审计同成同败。
func (q *Queries) UpdateAdminProviderAccountWithAudit(ctx context.Context, arg UpdateAdminProviderAccountWithAuditParams) (AdminProviderAccountRow, error) {
	if q == nil {
		return AdminProviderAccountRow{}, ErrProviderAccountBulkTransactionUnavailable
	}
	beginner, ok := q.db.(providerAccountBulkTxBeginner)
	if !ok {
		return AdminProviderAccountRow{}, ErrProviderAccountBulkTransactionUnavailable
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return AdminProviderAccountRow{}, err
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()

	txq := q.WithTx(tx)
	row, err := txq.UpdateAdminProviderAccount(ctx, arg.Update)
	if err != nil {
		return AdminProviderAccountRow{}, err
	}
	if _, err := txq.InsertAdminAuditEvent(ctx, arg.Audit); err != nil {
		return AdminProviderAccountRow{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AdminProviderAccountRow{}, err
	}
	return row, nil
}
