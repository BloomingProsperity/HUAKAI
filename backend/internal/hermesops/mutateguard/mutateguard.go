// HUAKAI · iKun

// Package mutateguard 给 Hermes 的 MUTATING 路径设界,这样一阵已确认的 mutation 风暴就无法耗尽
// 共享的 pgxpool 连接 / advisory-lock 槽位、把核心网关拖垮(审计 B4/B5)。
//
// 它携带两个协作的、ADDITIVE(可叠加)的 guard,每个都带一个禁用哨兵,这样未设置的部署逐字节
// 就是旧的无上限行为:
//
//   - 一个进程级并发 Semaphore,限制同时可有多少 mutation 持有一个连接池连接(在 BeginTx
//     BEFORE(之前)获取,这样上限约束的是已持有的连接数,而非在等待的连接数),以及
//   - 一个每运营者 token 的滑动窗口 RateLimiter(仿照 internal/loginthrottle/limiter.go:
//     MaxKeys fail-closed + 一个注入的 Now 以做确定性测试),这样单个运营者 token 就无法占用整个
//     mutating 预算。
//
// 两者都是内存中的单进程结构;多副本部署会在其之上再叠一层中心化限流器(后续跟进),正如
// loginthrottle 对它自己的 IP 桶所注。
package mutateguard

import (
	"context"
	"errors"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
)

// ErrBusy 在 mutating 并发上限饱和、且有界的获取窗口在某个槽位空出之前已过去时,由
// Semaphore.Acquire 返回。它是一个干净的"稍后再试"信号(在上游映射到 HTTP 429),绝不是挂起。
var ErrBusy = errors.New("mutateguard: mutating concurrency saturated")

// Semaphore 在进程级限制并发的 mutating 执行数。nil 或 size 为零/负的 Semaphore 是 DISABLED
// (禁用)哨兵:Acquire 是即时 no-op、Release 什么也不做,复现旧的无上限行为。
type Semaphore struct {
	sem    *semaphore.Weighted
	enable bool
}

// NewSemaphore 构造一个给定 size 的并发上限。size <= 0 禁用它(无上限 / 旧行为)——调用方原样
// 传入已解析的 knob 默认值。
func NewSemaphore(size int) *Semaphore {
	if size <= 0 {
		return &Semaphore{enable: false}
	}
	return &Semaphore{sem: semaphore.NewWeighted(int64(size)), enable: true}
}

// Acquire 预留一个槽位,最多等待 acquireWait 以等到一个空出。成功时它返回一个调用方 MUST
// (必须)defer 的 release 函数。当 guard 被禁用时它立即返回一个 no-op 的 release。超时时它返回
// ErrBusy(绝不阻塞超过 acquireWait)。负的 acquireWait 被视为只用调用方父 ctx 的截止(无额外界)。
func (s *Semaphore) Acquire(ctx context.Context, acquireWait time.Duration) (release func(), err error) {
	if s == nil || !s.enable {
		return func() {}, nil
	}
	acqCtx := ctx
	if acquireWait > 0 {
		var cancel context.CancelFunc
		acqCtx, cancel = context.WithTimeout(ctx, acquireWait)
		defer cancel()
	}
	if err := s.sem.Acquire(acqCtx, 1); err != nil {
		// Acquire 只在 ctx 取消/截止时失败——呈现一个干净的 busy 信号而非原始 context error,
		// 好让 handler 把它映射到 429。
		return func() {}, ErrBusy
	}
	released := false
	return func() {
		if released {
			return
		}
		released = true
		s.sem.Release(1)
	}, nil
}

// Enabled 报告并发上限是否处于激活(size > 0)。
func (s *Semaphore) Enabled() bool { return s != nil && s.enable }

// RateLimiter 是一个每键的滑动窗口计数器。nil 或 limit 非正的 RateLimiter 是 DISABLED(禁用)
// 哨兵:Allow 始终返回 true。它以运营者 token id 为键,这样预算是按每个运营者,而非按每个租户。
type RateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	maxKeys int
	now     func() time.Time
	hits    map[string][]time.Time
	enable  bool
}

// NewRateLimiter 构造一个滑动窗口限流器,每键在每个 `window` 内允许 `limit` 个事件。limit <= 0
// 禁用它(旧行为 / 无上限)。maxKeys 限制被跟踪的键数(超出即 fail-closed,镜像
// loginthrottle.MaxKeys);<= 0 退回到一个合理默认。now 为 nil 时默认 time.Now(测试注入一个时钟)。
func NewRateLimiter(limit int, window time.Duration, maxKeys int, now func() time.Time) *RateLimiter {
	if limit <= 0 {
		return &RateLimiter{enable: false}
	}
	if window <= 0 {
		window = time.Minute
	}
	if maxKeys <= 0 {
		maxKeys = 100000
	}
	if now == nil {
		now = time.Now
	}
	return &RateLimiter{
		limit:   limit,
		window:  window,
		maxKeys: maxKeys,
		now:     now,
		hits:    make(map[string][]time.Time),
		enable:  true,
	}
}

// Allow 为 key 记录一个事件,并报告它是否在预算之内。超出预算时它 NOT(不)记录该事件(这样一次
// 被拒绝的尝试不会把窗口进一步往后推),并返回一个粗略的 RetryAfter。被禁用的限流器始终允许。
// fail-closed:若被跟踪键表已满且 key 是新的,为保护内存而拒绝该请求(与 loginthrottle 姿态相同)。
func (l *RateLimiter) Allow(key string) (allowed bool, retryAfter time.Duration) {
	if l == nil || !l.enable {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cutoff := now.Add(-l.window)

	stamps, known := l.hits[key]
	if !known {
		if len(l.hits) >= l.maxKeys && !l.evictLocked(cutoff) {
			// 内存保护:拒绝一个新键,而非无界增长。
			return false, l.window
		}
	}
	kept := pruneLocked(stamps, cutoff)
	if len(kept) >= l.limit {
		// 超出预算:保留已裁剪的切片(不记录),这样一次拒绝永远不会延长窗口,并呈现一个粗略的
		// 重试提示。
		l.hits[key] = kept
		return false, l.window
	}
	kept = append(kept, now)
	l.hits[key] = kept
	return true, 0
}

// Enabled 报告该限流器是否处于激活(limit > 0)。
func (l *RateLimiter) Enabled() bool { return l != nil && l.enable }

// pruneLocked 返回比 cutoff 更新的时间戳,复用底层数组。
func pruneLocked(stamps []time.Time, cutoff time.Time) []time.Time {
	if len(stamps) == 0 {
		return stamps[:0]
	}
	kept := stamps[:0]
	for _, t := range stamps {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	return kept
}

// evictLocked 丢弃那些每个时间戳都已老化到窗口之外的键,为一个新键腾出空间。若至少回收了一个键
// 则返回 true。调用时持有 mutex。
func (l *RateLimiter) evictLocked(cutoff time.Time) bool {
	evicted := false
	for k, stamps := range l.hits {
		if len(pruneLocked(stamps, cutoff)) == 0 {
			delete(l.hits, k)
			evicted = true
		}
	}
	return evicted
}
