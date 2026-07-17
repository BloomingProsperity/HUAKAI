package billing

import (
	"context"
	"errors"
	"expvar"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	dbbillingrecovery "github.com/BloomingProsperity/HUAKAI/internal/db/billingrecovery"
)

const (
	abortLeaseExpediteTimeout = time.Second
	billingAbortMetricsName   = "billing_abort"
)

const (
	billingAbortOutcomeRetrySuccess   = "retry_success"
	billingAbortOutcomeExhausted      = "exhausted"
	billingAbortOutcomeExpediteFailed = "expedite_failed"
)

var (
	billingAbortMetricsOnce sync.Once
	billingAbortMetrics     *expvar.Map
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
	policy txRetryPolicy,
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
	if policy.attempts <= 0 {
		policy = settleTx2RetryPolicy
	}
	if policy.backoffBase <= 0 {
		policy.backoffBase = reserveBackoffBase
	}
	if policy.backoffCap < policy.backoffBase {
		policy.backoffCap = policy.backoffBase
	}
	prev := policy.backoffBase
	var lastErr error
	lastSQLState := "none"
	for attempt := 1; attempt <= policy.attempts; attempt++ {
		err := fn(ctx)
		if err == nil {
			if name == "abort" && attempt > 1 {
				observeBillingAbortResilience(lastSQLState, billingAbortOutcomeRetrySuccess, attempt)
			}
			return nil
		}
		if !isReserveSerializationConflict(err) {
			return err
		}
		lastErr = err
		lastSQLState = billingRetrySQLState(err)
		if attempt == policy.attempts {
			slog.WarnContext(ctx, "billing Tx2 serialization retry exhausted",
				slog.String("operation", name),
				slog.Int("attempts", attempt))
			if name == "abort" {
				observeBillingAbortResilience(lastSQLState, billingAbortOutcomeExhausted, attempt)
			}
			return lastErr
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		prev = txRetryBackoff(policy, prev, rnd)
		if !sleep(ctx, prev) {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return lastErr
		}
	}
	return lastErr
}

// expediteAbortLeaseAfterConflict 只在 Tx2 冲突预算耗尽后建立下一轮清扫资格。
// 清理写不参与主错误裁决，避免恢复通道故障掩盖真正的终结失败。
func (s *DefaultSettler) expediteAbortLeaseAfterConflict(ctx context.Context, tenantID, claimID int64, generation abortClaimGeneration, primaryErr error) error {
	if !isReserveSerializationConflict(primaryErr) {
		return primaryErr
	}
	// 没有成功读到 claim 就没有可证明的代际，安全无为比误伤后继 attempt 更重要。
	if !generation.observed {
		return primaryErr
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), abortLeaseExpediteTimeout)
	defer cancel()

	var expediteErr error
	if s == nil || s.abortRecoveryQ == nil {
		expediteErr = errors.New("billing abort recovery query is not configured")
	} else {
		_, expediteErr = s.abortRecoveryQ.ExpediteAbortLease(cleanupCtx, dbbillingrecovery.ExpediteAbortLeaseParams{
			TenantID:   tenantID,
			ClaimID:    claimID,
			AttemptSeq: generation.attemptSeq,
		})
	}
	if expediteErr == nil {
		return primaryErr
	}

	sqlState := billingRetrySQLState(primaryErr)
	observeBillingAbortResilience(sqlState, billingAbortOutcomeExpediteFailed, 0)
	slog.WarnContext(cleanupCtx, "billing abort lease expedite failed",
		slog.String("operation", "abort"),
		slog.String("outcome", billingAbortOutcomeExpediteFailed),
		slog.String("sqlstate", sqlState),
		slog.String("error_class", billingAbortExpediteErrorClass(expediteErr)))
	return primaryErr
}

func billingAbortExpediteErrorClass(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return "postgres"
	}
	return "other"
}

func billingRetrySQLState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "40001", "40P01":
			return pgErr.Code
		}
	}
	return "none"
}

func observeBillingAbortResilience(sqlState, outcome string, attempts int) {
	if sqlState != "40001" && sqlState != "40P01" && sqlState != "none" {
		sqlState = "none"
	}
	switch outcome {
	case billingAbortOutcomeRetrySuccess, billingAbortOutcomeExhausted, billingAbortOutcomeExpediteFailed:
	default:
		return
	}
	billingAbortMetricsOnce.Do(func() {
		if existing := expvar.Get(billingAbortMetricsName); existing != nil {
			billingAbortMetrics, _ = existing.(*expvar.Map)
			return
		}
		billingAbortMetrics = expvar.NewMap(billingAbortMetricsName)
	})
	if billingAbortMetrics == nil {
		return
	}
	key := fmt.Sprintf("sqlstate=%s|outcome=%s", sqlState, outcome)
	if attempts > 0 {
		key += fmt.Sprintf("|attempts=%d", attempts)
	}
	billingAbortMetrics.Add(key, 1)
}
