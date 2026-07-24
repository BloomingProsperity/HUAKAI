// A07.1：TokenBucket 原语，用于速率预算的纯数据结构。
// 当前健康与限流合同见 docs/HUAKAI工程设计手册.md §10。
//
// 这是 A07 三作用域刷新风暴控制器的地基原语。A07.2(singleflight)
// 与 A07.3(三作用域策略合成器)会在后续的原子提交里基于此原语构建。
// A07.4 把它接到 F-AUTH-005。
//
// 无 IO、无网络、不接触凭证:纯算法 + 状态。
package gateway

import (
	"sync"
	"time"
)

// uninitializedRefillNs 是一个哨兵值,表示该桶从未在任何挂钟时间被观测过。
// 第一次调用方法时会把 lastRefillNs 初始化为那次调用的 `now`,这样该类型
// 在构造时无需依赖 time.Now() 即可完全测试。
const uninitializedRefillNs int64 = -1 << 63

// TokenBucket 是经典的按需补充式令牌桶限流器。
// Rate 为每秒补充的令牌数;Burst 为容量上限。
// 所有方法均可并发安全使用。
type TokenBucket struct {
	Rate  float64 // 每秒补充的令牌数
	Burst float64 // 最大令牌容量

	mu           sync.Mutex
	tokens       float64
	lastRefillNs int64 // 上次补充的 Unix 纳秒,或 uninitializedRefillNs
}

// NewTokenBucket 返回一个按给定 rate 与 burst 装满的 TokenBucket。
// 负数输入会夹到 0(退化但定义明确:一个永远无法补充的耗尽桶 —— 可用作
// 「全部拒绝」的哨兵)。
func NewTokenBucket(rate, burst float64) *TokenBucket {
	if rate < 0 {
		rate = 0
	}
	if burst < 0 {
		burst = 0
	}
	return &TokenBucket{
		Rate:         rate,
		Burst:        burst,
		tokens:       burst,
		lastRefillNs: uninitializedRefillNs,
	}
}

// TryAcquire 尝试在给定时间消耗 1 个令牌。
// 当且仅当有可用令牌时返回 true。
func (b *TokenBucket) TryAcquire(now time.Time) bool {
	return b.TryAcquireN(now, 1)
}

// TryAcquireN 尝试在给定时间消耗 n 个令牌。
// n < 0  → 返回 false(非法输入)。
// n == 0 → 补充后返回 true(空操作)。
// n > Burst → 返回 false(永远无法满足)。
// 其余情况 → 当且仅当桶里至少有 n 个令牌时返回 true。
func (b *TokenBucket) TryAcquireN(now time.Time, n float64) bool {
	if n < 0 {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	b.refillLocked(now)
	if n == 0 {
		return true
	}
	if n > b.Burst || b.tokens < n {
		return false
	}
	b.tokens -= n
	return true
}

// NextAvailableAt 返回最早会有 1 个令牌可用的时间。若已有可用令牌,
// 则返回 now。
// 若该桶永远无法满足 1 个令牌(Rate==0 且桶为空,或 Burst<1),则返回
// 零值 time.Time,以便调用方识别「永不」。
func (b *TokenBucket) NextAvailableAt(now time.Time) time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.refillLocked(now)
	if b.tokens >= 1 {
		return now
	}
	if b.Burst < 1 || b.Rate <= 0 {
		return time.Time{}
	}
	missing := 1 - b.tokens
	ns := int64(missing / b.Rate * float64(time.Second))
	// 向上取整:避免返回一个那时只补充了 0.999... 个令牌的时间。
	if float64(ns)/float64(time.Second)*b.Rate < missing {
		ns++
	}
	if ns < 0 {
		ns = 0
	}
	return now.Add(time.Duration(ns))
}

// Refund 把 1 个令牌还回桶里 —— 用于:占用了名额后上游调用失败,
// 调用方不希望浪费预算的场景。令牌数会夹到 Burst。
func (b *TokenBucket) Refund(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.refillLocked(now)
	if b.Burst <= 0 {
		b.tokens = 0
		return
	}
	b.tokens++
	if b.tokens > b.Burst {
		b.tokens = b.Burst
	}
}

// Snapshot 返回当前令牌数与上次补充的时间。
// 供指标与调试用;不用于路由决策(请用 TryAcquire)。
// 若该桶从未被观测过,lastRefillAt 为零值 time.Time。
func (b *TokenBucket) Snapshot() (tokens float64, lastRefillAt time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lastRefillNs == uninitializedRefillNs {
		return b.tokens, time.Time{}
	}
	return b.tokens, time.Unix(0, b.lastRefillNs)
}

// refillLocked 把自 lastRefillNs 以来攒下的令牌补充进来,上限为 Burst。
// 调用方必须持有 b.mu。
func (b *TokenBucket) refillLocked(now time.Time) {
	nowNs := now.UnixNano()
	b.normalizeLocked()

	if b.lastRefillNs == uninitializedRefillNs {
		b.lastRefillNs = nowNs
		return
	}
	if nowNs <= b.lastRefillNs {
		// 时间倒退或相等:不补充,但也不要让游标回退。
		// 这样桶对偶尔倒走的测试时钟也保持健壮。
		return
	}
	if b.Rate > 0 && b.Burst > 0 {
		elapsedSeconds := float64(nowNs-b.lastRefillNs) / float64(time.Second)
		b.tokens += elapsedSeconds * b.Rate
	}
	b.lastRefillNs = nowNs
	b.normalizeLocked()
}

// normalizeLocked 把 tokens 夹到 [0, Burst]。调用方必须持有 b.mu。
func (b *TokenBucket) normalizeLocked() {
	if b.Burst <= 0 {
		b.tokens = 0
		return
	}
	if b.tokens < 0 {
		b.tokens = 0
	}
	if b.tokens > b.Burst {
		b.tokens = b.Burst
	}
}
