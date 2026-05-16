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

type BurstLimiter interface {
	AllowVoucherAttempt(context.Context, BurstAttempt) (BurstDecision, error)
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

func (l *MemoryBurstLimiter) AllowVoucherAttempt(_ context.Context, attempt BurstAttempt) (BurstDecision, error) {
	if l == nil {
		return BurstDecision{Allowed: true}, nil
	}
	now := attempt.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	key := burstKey(attempt)
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.state[key]
	if !st.blockedUntil.IsZero() && now.Before(st.blockedUntil) {
		return BurstDecision{Allowed: false, Attempts: st.attempts, WindowStart: st.windowStart, BlockedUntil: st.blockedUntil}, nil
	}
	windowStart := now.Truncate(l.policy.Window)
	if st.windowStart.IsZero() || !st.windowStart.Equal(windowStart) {
		st = burstState{windowStart: windowStart}
	}
	st.attempts++
	if st.attempts > l.policy.Limit {
		st.blockedUntil = now.Add(l.policy.BlockPeriod)
		l.state[key] = st
		return BurstDecision{Allowed: false, Attempts: st.attempts, WindowStart: st.windowStart, BlockedUntil: st.blockedUntil}, nil
	}
	l.state[key] = st
	return BurstDecision{Allowed: true, Attempts: st.attempts, WindowStart: st.windowStart}, nil
}

func burstKey(attempt BurstAttempt) string {
	return stringKey(attempt.TenantID, attempt.UserID, attempt.SourceIPHash)
}
