// HUAKAI · iKun

// Package loginthrottle 提供登录端点的「argon2 前置」限流闸(S2-048)。
//
// 它是 HUAKAI 融合三层防御里的 IP 维度两层:每 IP 并发 reservation 上限(挡住「N 个并发
// 请求在任一记录失败前全部冲进 argon2」的瞬时 CPU 放大)+ 滑动窗口失败计数 + 失败过多后的
// 限时封禁。账号维度的锁定仍由 userauth 维护,不在本包。
//
// 关键不变量:Begin 在「查用户 / 跑 argon2」之前调用 —— 命中即拒(429),绝不进 KDF。
// 这样未认证攻击者无法用任意邮箱触发昂贵的 argon2。所有时间走注入的 Now,便于判别测试。
package loginthrottle

import (
	"sync"
	"time"
)

// Reason 说明一次 Begin 被拒(或放行)的具体原因,用于审计/指标(对外响应仍是粗粒度 429,
// 不暴露细节)。
type Reason int

const (
	ReasonAllowed     Reason = iota // 放行
	ReasonIPInFlight                // 该 IP 并发 in-flight 已达上限
	ReasonIPWindow                  // 该 IP 窗口内(失败+in-flight)压力已达上限
	ReasonIPBanned                  // 该 IP 处于失败封禁期
	ReasonKeyPressure               // 限流器自身 key 数到顶,fail-closed 保护内存
)

func (r Reason) String() string {
	switch r {
	case ReasonAllowed:
		return "allowed"
	case ReasonIPInFlight:
		return "ip_in_flight"
	case ReasonIPWindow:
		return "ip_window"
	case ReasonIPBanned:
		return "ip_banned"
	case ReasonKeyPressure:
		return "key_pressure"
	default:
		return "unknown"
	}
}

// Decision 是 Begin 的判定结果。RetryAfter 仅在被拒时有意义,且粒度粗化(秒级),不携带
// 精确剩余次数或账号状态,避免侧信道。
type Decision struct {
	Allowed    bool
	Reason     Reason
	RetryAfter time.Duration
}

// Config 是限流参数。零值字段在 New 里填默认。
type Config struct {
	InFlightLimit int           // 每 IP 同时在途(已 Begin 未 commit)上限;直接卡并发 argon2
	Window        time.Duration // 失败滑动窗口长度
	WindowLimit   int           // 窗口内「失败 + in-flight」总量上限
	BanWindow     time.Duration // 统计失败以决定是否封禁的窗口
	BanAfter      int           // BanWindow 内失败达到此数 → 封禁
	BanDuration   time.Duration // 封禁时长
	InFlightTTL   time.Duration // in-flight reservation 最长存活(兜底回收泄漏的槽,如 panic 未 defer)
	MaxKeys       int           // 跟踪的 IP key 上限;到顶且无法回收则 fail-closed
	Now           func() time.Time
}

// DefaultConfig 是温和偏 NAT 友好的默认值(可由 cmd/gateway 用 env 覆盖)。
func DefaultConfig() Config {
	return Config{
		InFlightLimit: 4,
		Window:        time.Minute,
		WindowLimit:   10,
		BanWindow:     10 * time.Minute,
		BanAfter:      20,
		BanDuration:   15 * time.Minute,
		InFlightTTL:   2 * time.Minute,
		MaxKeys:       100000,
		Now:           time.Now,
	}
}

type bucket struct {
	failures     []time.Time
	inFlight     map[uint64]time.Time
	nextID       uint64
	blockedUntil time.Time
	lastSeen     time.Time
}

// Limiter 是并发安全的内存限流器。单进程语义:多副本部署需集中式(Redis)版本(follow-up)。
type Limiter struct {
	mu      sync.Mutex
	cfg     Config
	now     func() time.Time
	buckets map[string]*bucket
}

// New 用 cfg 构造 Limiter,零值字段回落到 DefaultConfig。
func New(cfg Config) *Limiter {
	d := DefaultConfig()
	if cfg.InFlightLimit <= 0 {
		cfg.InFlightLimit = d.InFlightLimit
	}
	if cfg.Window <= 0 {
		cfg.Window = d.Window
	}
	if cfg.WindowLimit <= 0 {
		cfg.WindowLimit = d.WindowLimit
	}
	if cfg.BanWindow <= 0 {
		cfg.BanWindow = d.BanWindow
	}
	if cfg.BanAfter <= 0 {
		cfg.BanAfter = d.BanAfter
	}
	if cfg.BanDuration <= 0 {
		cfg.BanDuration = d.BanDuration
	}
	if cfg.InFlightTTL <= 0 {
		cfg.InFlightTTL = d.InFlightTTL
	}
	if cfg.MaxKeys <= 0 {
		cfg.MaxKeys = d.MaxKeys
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Limiter{cfg: cfg, now: cfg.Now, buckets: make(map[string]*bucket)}
}

// Lease 代表一次被放行的尝试持有的 in-flight reservation。处理结束后必须恰好 commit 一次:
// Success(登录成功,释放槽,不计失败)/ Failure(登录失败,释放槽并计入失败,可能触发封禁)。
// Cancel 是兜底(defer),在已 commit 后为 no-op,用于 panic/早退释放泄漏的槽。被拒的 Begin
// 返回一个空 Lease,其所有方法都是 no-op。
type Lease struct {
	l         *Limiter
	key       string
	id        uint64
	reserved  bool // 该 Lease 是否真持有一个 reservation(被拒的 Begin 为 false)
	committed bool
}

// Begin 在「跑 argon2 之前」调用。放行则返回 Decision{Allowed:true} 与一个持有 in-flight
// 槽的 Lease;被拒则 Allowed=false 且 Lease 为 no-op。调用方应 `defer lease.Cancel()`。
func (l *Limiter) Begin(key string) (*Lease, Decision) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()

	b := l.buckets[key]
	if b == nil {
		// 新 key:先做 key 压力检查(可能触发回收),腾不出空间则 fail-closed。
		if len(l.buckets) >= l.cfg.MaxKeys && !l.evictLocked(now) {
			return l.deniedLease(), Decision{Allowed: false, Reason: ReasonKeyPressure, RetryAfter: time.Second}
		}
		b = &bucket{inFlight: make(map[uint64]time.Time)}
		l.buckets[key] = b
	}
	b.lastSeen = now
	l.pruneBucketLocked(b, now)

	if now.Before(b.blockedUntil) {
		return l.deniedLease(), Decision{Allowed: false, Reason: ReasonIPBanned, RetryAfter: ceilSeconds(b.blockedUntil.Sub(now))}
	}
	if len(b.inFlight) >= l.cfg.InFlightLimit {
		return l.deniedLease(), Decision{Allowed: false, Reason: ReasonIPInFlight, RetryAfter: time.Second}
	}
	if l.failuresWithinLocked(b, now, l.cfg.Window)+len(b.inFlight) >= l.cfg.WindowLimit {
		return l.deniedLease(), Decision{Allowed: false, Reason: ReasonIPWindow, RetryAfter: ceilSeconds(l.cfg.Window)}
	}

	b.nextID++
	id := b.nextID
	b.inFlight[id] = now
	return &Lease{l: l, key: key, id: id, reserved: true}, Decision{Allowed: true, Reason: ReasonAllowed}
}

func (l *Limiter) deniedLease() *Lease {
	// 被拒不持有 reservation;committed=true 让所有 commit 方法直接 no-op。
	return &Lease{l: l, committed: true}
}

// Success 释放 in-flight 槽,且不记失败(成功登录不消耗失败窗口/不触发封禁)。
func (le *Lease) Success() {
	if le == nil || le.l == nil {
		return
	}
	le.l.mu.Lock()
	defer le.l.mu.Unlock()
	if le.committed || !le.reserved {
		le.committed = true
		return
	}
	le.committed = true
	if b := le.l.buckets[le.key]; b != nil {
		delete(b.inFlight, le.id)
	}
}

// Failure 释放 in-flight 槽,记一次失败,并在 BanWindow 内失败达阈时设置封禁。
func (le *Lease) Failure() {
	if le == nil || le.l == nil {
		return
	}
	le.l.mu.Lock()
	defer le.l.mu.Unlock()
	if le.committed || !le.reserved {
		le.committed = true
		return
	}
	le.committed = true
	b := le.l.buckets[le.key]
	if b == nil {
		return
	}
	delete(b.inFlight, le.id)
	now := le.l.now()
	b.failures = append(b.failures, now)
	le.l.pruneBucketLocked(b, now)
	if le.l.failuresWithinLocked(b, now, le.l.cfg.BanWindow) >= le.l.cfg.BanAfter {
		b.blockedUntil = now.Add(le.l.cfg.BanDuration)
	}
}

// Cancel 仅释放 in-flight 槽(不计失败);已 commit 后 no-op。用于 defer 兜底。
func (le *Lease) Cancel() {
	if le == nil || le.l == nil {
		return
	}
	le.l.mu.Lock()
	defer le.l.mu.Unlock()
	if le.committed || !le.reserved {
		le.committed = true
		return
	}
	le.committed = true
	if b := le.l.buckets[le.key]; b != nil {
		delete(b.inFlight, le.id)
	}
}

// pruneBucketLocked 丢弃超出统计窗口的失败时间戳与超龄(泄漏)的 in-flight 槽。
func (l *Limiter) pruneBucketLocked(b *bucket, now time.Time) {
	keep := l.cfg.Window
	if l.cfg.BanWindow > keep {
		keep = l.cfg.BanWindow
	}
	cutoff := now.Add(-keep)
	if len(b.failures) > 0 {
		kept := b.failures[:0]
		for _, t := range b.failures {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		b.failures = kept
	}
	if len(b.inFlight) > 0 {
		ttlCutoff := now.Add(-l.cfg.InFlightTTL)
		for id, start := range b.inFlight {
			if start.Before(ttlCutoff) {
				delete(b.inFlight, id)
			}
		}
	}
}

func (l *Limiter) failuresWithinLocked(b *bucket, now time.Time, window time.Duration) int {
	cutoff := now.Add(-window)
	n := 0
	for _, t := range b.failures {
		if t.After(cutoff) {
			n++
		}
	}
	return n
}

// evictLocked 尝试回收「无 in-flight、无有效封禁、且早于最长统计窗口未活动」的旧 bucket。
// 回收到至少一个则返回 true。在 mu 已持有时调用。
func (l *Limiter) evictLocked(now time.Time) bool {
	keep := l.cfg.Window
	if l.cfg.BanWindow > keep {
		keep = l.cfg.BanWindow
	}
	cutoff := now.Add(-keep)
	evicted := false
	for k, b := range l.buckets {
		if len(b.inFlight) == 0 && !now.Before(b.blockedUntil) && b.lastSeen.Before(cutoff) {
			delete(l.buckets, k)
			evicted = true
		}
	}
	return evicted
}

func ceilSeconds(d time.Duration) time.Duration {
	if d <= 0 {
		return time.Second
	}
	return ((d + time.Second - 1) / time.Second) * time.Second
}
