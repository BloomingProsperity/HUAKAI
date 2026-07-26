package tenantadmin

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	serializableMutationAttempts = 5
	serializableMutationBaseWait = 2 * time.Millisecond
	serializableMutationMaxWait  = 25 * time.Millisecond
)

// runSerializableMutation 只重跑 PostgreSQL 已完整回滚的序列化冲突或死锁。
// 确定性业务错误立即返回，避免把版本冲突和权限拒绝伪装成瞬时故障。
func runSerializableMutation(
	ctx context.Context,
	pool *pgxpool.Pool,
	fn func(pgx.Tx) error,
) error {
	if pool == nil || fn == nil {
		return ErrNotConfigured
	}
	var lastErr error
	for attempt := 0; attempt < serializableMutationAttempts; attempt++ {
		err := pgx.BeginTxFunc(
			ctx, pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, fn,
		)
		if err == nil || !isSerializableMutationConflict(err) {
			return err
		}
		lastErr = err
		if attempt+1 == serializableMutationAttempts {
			return lastErr
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		wait := serializableMutationBaseWait << attempt
		if wait > serializableMutationMaxWait {
			wait = serializableMutationMaxWait
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	return lastErr
}

func isSerializableMutationConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01")
}
