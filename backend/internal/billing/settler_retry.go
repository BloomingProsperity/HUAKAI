package billing

import (
	"context"
	"log/slog"
	"time"
)

// retryTx2 在完整 Tx2 事务遇到 Serializable 冲突时有限重试。
// fn 必须自包含 BeginTx→Commit/Rollback,所有 usage_record、billing_event、hold
// capture/release、pool slot 释放都必须在该事务内完成。PostgreSQL 对 40001/40P01
// 会撤销整事务,所以重跑不会留下部分写入;若并发路径已把 claim 推进到终态,
// settleOnce/abortOnce 的 status='reserving' 守卫会返回 ErrClaimNotReserving,
// 从而阻止重复计费、重复退款和重复审计事件。
func retryTx2(
	ctx context.Context,
	name string,
	fn func(context.Context) error,
	sleep func(context.Context, time.Duration) bool,
	rnd func(int64) int64,
) error {
	if sleep == nil {
		sleep = sleepWithContext
	}
	if rnd == nil {
		rnd = defaultReserveRand
	}
	if name == "" {
		name = "tx2"
	}
	prev := reserveBackoffBase
	var lastErr error
	for attempt := 0; ; attempt++ {
		err := fn(ctx)
		if err == nil || !isReserveSerializationConflict(err) {
			return err
		}
		lastErr = err
		if attempt >= reserveRetryMax {
			slog.WarnContext(ctx, "billing Tx2 serialization retry exhausted",
				slog.String("operation", name),
				slog.Int("attempts", attempt+1))
			return lastErr
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		prev = reserveBackoff(prev, rnd)
		if !sleep(ctx, prev) {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return lastErr
		}
	}
}
