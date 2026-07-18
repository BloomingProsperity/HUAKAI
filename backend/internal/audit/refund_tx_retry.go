package audit

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	refundTransactionRetryAttempts = 6
	refundTransactionBackoffBase   = 2 * time.Millisecond
	refundTransactionBackoffCap    = 50 * time.Millisecond
)

// retryRefundTransaction 只重跑被 PostgreSQL 判定为可重试的完整原子事务。
// 业务错误、存储不可用和收据签名失败立即返回，避免把确定性故障伪装成瞬时冲突。
func retryRefundTransaction(ctx context.Context, run func(context.Context) error) error {
	if run == nil {
		return errors.New("audit: refund transaction runner required")
	}
	previous := refundTransactionBackoffBase
	var lastErr error
	for attempt := 1; attempt <= refundTransactionRetryAttempts; attempt++ {
		err := run(ctx)
		if err == nil || !isRefundTransactionConflict(err) {
			return err
		}
		lastErr = err
		if attempt == refundTransactionRetryAttempts {
			slog.WarnContext(ctx, "audit refund transaction retry exhausted", slog.Int("attempts", attempt))
			return lastErr
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		previous = refundTransactionBackoff(previous)
		timer := time.NewTimer(previous)
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

func isRefundTransactionConflict(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "40001" || pgErr.Code == "40P01"
}

func refundTransactionBackoff(previous time.Duration) time.Duration {
	high := previous * 3
	if high < refundTransactionBackoffBase {
		high = refundTransactionBackoffBase
	}
	delay := refundTransactionBackoffBase
	if span := int64(high - refundTransactionBackoffBase); span > 0 {
		delay += time.Duration(rand.Int63n(span))
	}
	if delay > refundTransactionBackoffCap {
		return refundTransactionBackoffCap
	}
	return delay
}
