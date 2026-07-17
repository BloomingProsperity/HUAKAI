package voucher

import (
	"context"
	"time"
)

// burstCounterStore 是 RedisBurstLimiter 依赖的窄计数后端:按 key 读当前失败计数、按 key 增计数并(首次)
// 设窗口 TTL。生产实现包一层 redis 客户端(见 cmd/gateway);测试用内存替身——这样限流逻辑可脱离真实 Redis 单测,
// 且 voucher 包不直接依赖 go-redis。
type burstCounterStore interface {
	// Count 返回 key 当前失败计数(不存在返回 0)。
	Count(ctx context.Context, key string) (int64, error)
	// IncrementWithTTL 把 key 计数 +1;首次创建时按 ttl 设过期(窗口到期自动重置)。返回 +1 后的值。
	IncrementWithTTL(ctx context.Context, key string, ttl time.Duration) (int64, error)
}

// RedisBurstLimiter 用共享计数后端实现【跨实例】的"只计失败"反猜码限流：
//   - CheckVoucherBurst 只读计数，达上限即拒；后端出错时 fail-open 放行，
//     绝不因限流后端故障误伤合法兑换；
//   - RecordVoucherFailure 仅在猜码类失败时由 service 调用,增计数并按 Window 设 TTL(过期即自动重置窗口)。
//
// 与单进程的 MemoryBurstLimiter 同实现 BurstLimiter 接口,可由 wiring 按是否配置 Redis 二选一注入。
type RedisBurstLimiter struct {
	store  burstCounterStore
	policy BurstPolicy
}

var _ BurstLimiter = (*RedisBurstLimiter)(nil)

func NewRedisBurstLimiter(store burstCounterStore, policy BurstPolicy) *RedisBurstLimiter {
	if policy.Limit <= 0 {
		policy.Limit = DefaultBurstPolicy().Limit
	}
	if policy.Window <= 0 {
		policy.Window = DefaultBurstPolicy().Window
	}
	return &RedisBurstLimiter{store: store, policy: policy}
}

func (l *RedisBurstLimiter) CheckVoucherBurst(ctx context.Context, attempt BurstAttempt) (BurstDecision, error) {
	if l == nil || l.store == nil {
		return BurstDecision{Allowed: true}, nil
	}
	count, err := l.store.Count(ctx, redisBurstKey(attempt))
	if err != nil {
		// 后端不可用时 fail-open，绝不因限流故障阻断合法用户兑换。
		return BurstDecision{Allowed: true}, nil
	}
	if count >= int64(l.policy.Limit) {
		return BurstDecision{Allowed: false, Attempts: int(count)}, nil
	}
	return BurstDecision{Allowed: true, Attempts: int(count)}, nil
}

func (l *RedisBurstLimiter) RecordVoucherFailure(ctx context.Context, attempt BurstAttempt) error {
	if l == nil || l.store == nil {
		return nil
	}
	_, err := l.store.IncrementWithTTL(ctx, redisBurstKey(attempt), l.policy.Window)
	return err
}

func redisBurstKey(attempt BurstAttempt) string {
	return "voucher:burst:" + burstKey(attempt)
}
