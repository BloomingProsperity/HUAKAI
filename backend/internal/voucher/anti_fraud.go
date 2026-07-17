package voucher

import (
	"context"
	"sync"
	"time"
)

type BurstPolicy struct {
	Limit       int
	Window      time.Duration
	BlockPeriod time.Duration
}

func DefaultBurstPolicy() BurstPolicy {
	return BurstPolicy{Limit: 5, Window: time.Minute, BlockPeriod: time.Minute}
}

type BurstAttempt struct {
	TenantID        int64
	UserID          int64
	SourceIPHash    string
	CodeFingerprint string
	RequestID       string
	Now             time.Time
}

type BurstDecision struct {
	Allowed      bool
	Attempts     int
	WindowStart  time.Time
	BlockedUntil time.Time
}

// BurstLimiter 把"判定"与"计数"分开，实现"只计失败"的反猜码限流：
//   - CheckVoucherBurst 只读判定该 (租户,用户,源IP) 近窗失败次数是否已达上限,绝不增计数;
//   - RecordVoucherFailure 仅在兑换确因猜码类失败时调用,增计数。
//
// 这样成功兑换(合法用户)永不推高计数,避免连续兑多张有效码被误限;只有反复猜错码者才会被拉闸。
type BurstLimiter interface {
	CheckVoucherBurst(context.Context, BurstAttempt) (BurstDecision, error)
	RecordVoucherFailure(context.Context, BurstAttempt) error
}

type MemoryBurstLimiter struct {
	mu     sync.Mutex
	policy BurstPolicy
	state  map[string]burstState
}

type burstState struct {
	windowStart  time.Time
	attempts     int
	blockedUntil time.Time
}

func NewMemoryBurstLimiter(policy BurstPolicy) *MemoryBurstLimiter {
	if policy.Limit <= 0 {
		policy.Limit = DefaultBurstPolicy().Limit
	}
	if policy.Window <= 0 {
		policy.Window = DefaultBurstPolicy().Window
	}
	if policy.BlockPeriod <= 0 {
		policy.BlockPeriod = DefaultBurstPolicy().BlockPeriod
	}
	return &MemoryBurstLimiter{policy: policy, state: map[string]burstState{}}
}

// CheckVoucherBurst 只读判定:被拉黑期内、或当前窗失败计数已达上限 → Allowed=false;否则放行。不增计数。
func (l *MemoryBurstLimiter) CheckVoucherBurst(_ context.Context, attempt BurstAttempt) (BurstDecision, error) {
	if l == nil {
		return BurstDecision{Allowed: true}, nil
	}
	now := burstNow(attempt)
	key := burstKey(attempt)
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.state[key]
	if !st.blockedUntil.IsZero() && now.Before(st.blockedUntil) {
		return BurstDecision{Allowed: false, Attempts: st.attempts, WindowStart: st.windowStart, BlockedUntil: st.blockedUntil}, nil
	}
	windowStart := now.Truncate(l.policy.Window)
	if st.windowStart.IsZero() || !st.windowStart.Equal(windowStart) {
		// 新窗:失败计数视为 0,放行。
		return BurstDecision{Allowed: true, WindowStart: windowStart}, nil
	}
	if st.attempts >= l.policy.Limit {
		return BurstDecision{Allowed: false, Attempts: st.attempts, WindowStart: st.windowStart, BlockedUntil: st.blockedUntil}, nil
	}
	return BurstDecision{Allowed: true, Attempts: st.attempts, WindowStart: st.windowStart}, nil
}

// RecordVoucherFailure 记一次失败尝试:增当前窗失败计数;达上限即把该窗拉黑 BlockPeriod。
func (l *MemoryBurstLimiter) RecordVoucherFailure(_ context.Context, attempt BurstAttempt) error {
	if l == nil {
		return nil
	}
	now := burstNow(attempt)
	key := burstKey(attempt)
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.state[key]
	windowStart := now.Truncate(l.policy.Window)
	if st.windowStart.IsZero() || !st.windowStart.Equal(windowStart) {
		st = burstState{windowStart: windowStart}
	}
	st.attempts++
	if st.attempts >= l.policy.Limit {
		st.blockedUntil = now.Add(l.policy.BlockPeriod)
	}
	l.state[key] = st
	return nil
}

func burstNow(attempt BurstAttempt) time.Time {
	now := attempt.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return now
}

func burstKey(attempt BurstAttempt) string {
	return stringKey(attempt.TenantID, attempt.UserID, attempt.SourceIPHash)
}
