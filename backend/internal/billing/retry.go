package billing

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Serializable 预扣事务的重试默认参数。PostgreSQL 官方立场:Serializable 隔离下
// 40001 序列化失败是预期的、应由应用层重试的错误(40P01 死锁同理)。base 取小
// (常态几乎无争用、重试代价可忽略),decorrelated jitter 把同用户并发惊群在时间轴上
// 打散避免重试再撞,cap 封顶避免长尾延迟。5 次预算足以吸收现实并发突发。
const (
	reserveRetryMax    = 5
	reserveBackoffBase = 2 * time.Millisecond
	reserveBackoffCap  = 50 * time.Millisecond
	abortTx2Attempts   = 9
)

type txRetryPolicy struct {
	attempts    int
	backoffBase time.Duration
	backoffCap  time.Duration
}

var (
	// Settle 保持原六次预算；Abort 是终结路径，独立增加到九次以降低 hold 冻结概率，
	// 但不扩大单次退避上下界。
	settleTx2RetryPolicy = txRetryPolicy{
		attempts:    reserveRetryMax + 1,
		backoffBase: reserveBackoffBase,
		backoffCap:  reserveBackoffCap,
	}
	abortTx2RetryPolicy = txRetryPolicy{
		attempts:    abortTx2Attempts,
		backoffBase: reserveBackoffBase,
		backoffCap:  reserveBackoffCap,
	}
)

// isReserveSerializationConflict 判定错误是否为可安全重试的 Serializable 冲突
// (40001 序列化失败 / 40P01 死锁)。业务哨兵(ErrClaimRace / ErrFingerprintConflict /
// ErrInsufficientBalance / ErrTenantInactive)是 Go error 值而非 *pgconn.PgError,
// 天然不命中——它们是确定性业务结果,重试无意义、必须立即原样返回。
func isReserveSerializationConflict(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "40001" || pgErr.Code == "40P01"
}

// reserveBackoff 按 decorrelated jitter 计算下一次退避:sleep = min(cap, rand[base, prev*3])。
// 相邻重试的睡眠彼此去相关,把同用户并发惊群沿时间轴打散,比固定退避或纯指数退避
// 更快收敛(纯指数在惊群下每轮仍同步再撞)。这是相对项目内既有「立即无退避重试」
// 形态(mediatask/dispatcher)的算法升级。
func reserveBackoff(prev time.Duration, rnd func(int64) int64) time.Duration {
	return txRetryBackoff(settleTx2RetryPolicy, prev, rnd)
}

func txRetryBackoff(policy txRetryPolicy, prev time.Duration, rnd func(int64) int64) time.Duration {
	base := policy.backoffBase
	if base <= 0 {
		base = reserveBackoffBase
	}
	capDelay := policy.backoffCap
	if capDelay < base {
		capDelay = base
	}
	hi := prev * 3
	if hi < base {
		hi = base
	}
	span := int64(hi - base)
	d := base
	if span > 0 {
		d += time.Duration(rnd(span))
	}
	if d > capDelay {
		d = capDelay
	}
	return d
}

// retryReserve 在 Serializable 冲突上有限重试 fn(每次跑一整个干净事务)。约束:
//   - fn 必须自包含一个事务(BeginTx→Commit/Rollback);退避 sleep 发生在 fn 之外——
//     此时连接已归还连接池,不会 N 路并发各占一条连接睡眠打爆池。
//   - 只重试序列化冲突;业务哨兵与其它错误立即原样返回,不吞不改。
//   - 每轮先检查 ctx:取消/超时立即返回,不再睡眠。
//   - 预算耗尽后把冲突映射成 ErrClaimRace,调用方据此返回可重试的 409+Retry-After
//     (而非不透明 500),使用者可自动退避重试。
//
// sleep/rnd 为 nil 时用生产默认(真实睡眠 + math/rand)。
func retryReserve(
	ctx context.Context,
	fn func(context.Context) (*ReserveResult, error),
	sleep func(context.Context, time.Duration) bool,
	rnd func(int64) int64,
) (*ReserveResult, error) {
	if sleep == nil {
		sleep = sleepWithContext
	}
	if rnd == nil {
		rnd = defaultReserveRand
	}
	prev := reserveBackoffBase
	for attempt := 0; ; attempt++ {
		res, err := fn(ctx)
		if err == nil || !isReserveSerializationConflict(err) {
			return res, err
		}
		if attempt >= reserveRetryMax {
			// 耗尽退避预算=真实高并发争用信号,记 warn 供运营诊断(比不透明 500 好定位);
			// 降级成可重试的 ErrClaimRace,调用方返 409+Retry-After 让客户端自动退避重试。
			slog.WarnContext(ctx, "billing reserve serialization retry exhausted; degraded to retryable claim race",
				slog.Int("attempts", attempt+1))
			return nil, ErrClaimRace
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		prev = reserveBackoff(prev, rnd)
		if !sleep(ctx, prev) {
			return nil, ctx.Err()
		}
	}
}

func defaultReserveRand(n int64) int64 {
	if n <= 0 {
		return 0
	}
	return rand.Int63n(n)
}

// sleepWithContext 睡眠 d,或在 ctx 结束时提前返回。true=睡满,false=ctx 已取消。
func sleepWithContext(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
