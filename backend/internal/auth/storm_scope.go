package auth

import (
	"sync"
	"time"
)

// storm_scope.go 是三 scope refresh-storm controller 中内存版的
// endpoint + global 限速层。
//
// account scope (那道关键的同账号惊群 guard) 仍由 StormController.Acquire
// 做 DB 持久化 —— 持久且跨副本。本文件补上 A07 承诺的其余两个 scope:
//
//   - provider-endpoint: per (provider, endpoint) 一个 token bucket, 这样 M 个账号
//     同时过期也不会把某一家厂商的 OAuth token endpoint 冲垮。
//   - global: 一个进程级 bucket, 作为最后一道兜底上限。
//
// 本文件只保留单实例开发模式的内存实现。生产接线使用共享存储，使两个 scope
// 在所有副本间共同消费预算。
//
// 这些 bucket 是层内原语, 有意不复用请求路径上的
// gateway.TokenBucket: credential worker (以及它所依赖的这层 auth)
// 不得 import gateway 请求路径包, 后者同样是冻结的。其
// 算术就是标准的按需补充式 token bucket。

// StormScopeConfig 为 per-endpoint 和 global 两个 scope 设置 token rate
// (tokens/second) 和 burst (上限)。只有当 rate 为正且 burst 至少为一个完整
// token 时, 该 scope 才开启; 否则关闭 (admit-all) —— 本层是可选叠加式限流,
// 绝非静默阻塞, 因此零值/部分配置会退化为 "仅 account scope", 而不是
// 拒绝每一次 refresh。
type StormScopeConfig struct {
	PerEndpointRate  float64
	PerEndpointBurst float64
	GlobalRate       float64
	GlobalBurst      float64
}

func (c StormScopeConfig) endpointEnabled() bool {
	return c.PerEndpointRate > 0 && c.PerEndpointBurst >= 1
}

func (c StormScopeConfig) globalEnabled() bool {
	return c.GlobalRate > 0 && c.GlobalBurst >= 1
}

func (c StormScopeConfig) anyEnabled() bool {
	return c.endpointEnabled() || c.globalEnabled()
}

// stormScopeLimiter 持有内存版 bucket。值为 nil 的 *stormScopeLimiter
// 一律 admit (account-scope-only 部署)。
type stormScopeLimiter struct {
	cfg             StormScopeConfig
	globalBucket    *scopeBucket
	endpointBuckets sync.Map // map[string]*scopeBucket —— 惰性创建, 竞争安全
}

func newStormScopeLimiter(cfg StormScopeConfig) *stormScopeLimiter {
	if !cfg.anyEnabled() {
		return nil
	}
	l := &stormScopeLimiter{cfg: cfg}
	if cfg.globalEnabled() {
		l.globalBucket = newScopeBucket(cfg.GlobalRate, cfg.GlobalBurst)
	}
	return l
}

// endpointBucket 惰性为 key 创建 bucket, 通过 LoadOrStore 保证竞争安全。
func (l *stormScopeLimiter) endpointBucket(key string) *scopeBucket {
	if v, ok := l.endpointBuckets.Load(key); ok {
		return v.(*scopeBucket)
	}
	nb := newScopeBucket(l.cfg.PerEndpointRate, l.cfg.PerEndpointBurst)
	actual, _ := l.endpointBuckets.LoadOrStore(key, nb)
	return actual.(*scopeBucket)
}

// scopeBucket 是按需补充式 token bucket。rate 为 tokens/second, burst 为上限。
// 可并发安全使用。last-refill 时间为零值表示该 bucket 从未被观测过;
// 首次调用会把它锚定下来而不发放一次虚假补充, 这让该类型在注入时钟的
// 情况下完全可测。
type scopeBucket struct {
	rate  float64
	burst float64

	mu     sync.Mutex
	tokens float64
	last   time.Time
}

func newScopeBucket(rate, burst float64) *scopeBucket {
	if rate < 0 {
		rate = 0
	}
	if burst < 0 {
		burst = 0
	}
	return &scopeBucket{rate: rate, burst: burst, tokens: burst}
}

func (b *scopeBucket) refillLocked(now time.Time) {
	if b.last.IsZero() {
		b.last = now
		return
	}
	if !now.After(b.last) {
		// 时钟回拨或不变: 不补充, 但保留游标, 以便后续向前的 tick
		// 从这里开始测量经过的时间。
		return
	}
	if b.rate > 0 && b.burst > 0 {
		b.tokens += now.Sub(b.last).Seconds() * b.rate
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
	}
	b.last = now
}

// tryAcquire 在 now 时刻消费一个 token, 当且仅当有 token 可用时返回 true。
func (b *scopeBucket) tryAcquire(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refillLocked(now)
	if b.burst < 1 || b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// refund 退回一个 token (上限钳到 burst)。仅用于拒绝级联场景
// (endpoint 已准入, 随后 global 拒绝), 以免一次被拒绝的尝试浪费
// endpoint budget。它不会在 refresh 失败时被调用 —— 失败的尝试必须
// 保持其 token 已被消费, 否则就会重新打开 storm 窗口。
func (b *scopeBucket) refund(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refillLocked(now)
	if b.burst <= 0 {
		b.tokens = 0
		return
	}
	b.tokens++
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
}
