// A07.1: TokenBucket primitive — pure data structure for rate budgeting.
// Spec: docs/specs/upstream-credential-management.md §A07 / synthesis §1 A07.
//
// This is the foundation primitive for the A07 three-scope refresh storm
// controller. A07.2 (singleflight) and A07.3 (3-scope policy compositor) build
// on this primitive in subsequent atomic commits. A07.4 wires it to F-AUTH-005.
//
// No IO, no network, no credential contact: pure algorithm + state.
// Synthesis of two parallel-draft lanes (CLAUDE.md #10 + 2026-05-04 directive).
package gateway

import (
	"sync"
	"time"
)

// uninitializedRefillNs is a sentinel meaning the bucket has never been
// observed at any wall-clock time. The first call to a method initializes
// lastRefillNs to that call's `now`, which makes the type fully testable
// without relying on time.Now() during construction.
const uninitializedRefillNs int64 = -1 << 63

// TokenBucket is a classic refill-on-demand token-bucket rate limiter.
// Rate is tokens per second; Burst is the capacity ceiling.
// All methods are safe for concurrent use.
type TokenBucket struct {
	Rate  float64 // tokens refilled per second
	Burst float64 // maximum token capacity

	mu           sync.Mutex
	tokens       float64
	lastRefillNs int64 // Unix nanoseconds of last refill, or uninitializedRefillNs
}

// NewTokenBucket returns a full TokenBucket with the given rate and burst.
// Negative inputs clamp to 0 (degenerate but well-defined: an exhausted bucket
// that can never refill — useful as a "deny all" sentinel).
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

// TryAcquire attempts to consume 1 token at the given time.
// Returns true iff a token was available.
func (b *TokenBucket) TryAcquire(now time.Time) bool {
	return b.TryAcquireN(now, 1)
}

// TryAcquireN attempts to consume n tokens at the given time.
// n < 0  → returns false (invalid input).
// n == 0 → returns true after a refill (no-op).
// n > Burst → returns false (cannot ever satisfy).
// Otherwise → returns true iff the bucket holds at least n tokens.
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

// NextAvailableAt returns the earliest time at which 1 token will be
// available. If a token is already available it returns now.
// If the bucket cannot ever satisfy 1 token (Rate==0 with empty bucket, or
// Burst<1) it returns the zero time.Time so callers can detect "never".
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
	// Round up: avoid returning a time at which only 0.999... tokens would
	// have refilled.
	if float64(ns)/float64(time.Second)*b.Rate < missing {
		ns++
	}
	if ns < 0 {
		ns = 0
	}
	return now.Add(time.Duration(ns))
}

// Refund returns 1 token to the bucket — used when an upstream call failed
// after the slot was claimed and the caller wishes not to waste budget.
// Tokens are clamped to Burst.
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

// Snapshot returns the current token count and the time of the last refill.
// Intended for metrics and debug; not for routing decisions (use TryAcquire).
// If the bucket has never been observed, lastRefillAt is the zero time.Time.
func (b *TokenBucket) Snapshot() (tokens float64, lastRefillAt time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lastRefillNs == uninitializedRefillNs {
		return b.tokens, time.Time{}
	}
	return b.tokens, time.Unix(0, b.lastRefillNs)
}

// refillLocked adds tokens earned since lastRefillNs up to Burst.
// Caller must hold b.mu.
func (b *TokenBucket) refillLocked(now time.Time) {
	nowNs := now.UnixNano()
	b.normalizeLocked()

	if b.lastRefillNs == uninitializedRefillNs {
		b.lastRefillNs = nowNs
		return
	}
	if nowNs <= b.lastRefillNs {
		// Backward or equal time: no refill, but do not regress the cursor
		// either. This makes the bucket robust against test clocks that
		// occasionally tick backward.
		return
	}
	if b.Rate > 0 && b.Burst > 0 {
		elapsedSeconds := float64(nowNs-b.lastRefillNs) / float64(time.Second)
		b.tokens += elapsedSeconds * b.Rate
	}
	b.lastRefillNs = nowNs
	b.normalizeLocked()
}

// normalizeLocked clamps tokens into [0, Burst]. Caller must hold b.mu.
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
