// HUAKAI · iKun

// Package mutateguard bounds the Hermes MUTATING path so a burst of confirmed
// mutations cannot exhaust the shared pgxpool conns / advisory-lock slots and
// brown out the core gateway (audit B4/B5).
//
// It carries two cooperating, ADDITIVE guards, each with a disable sentinel so
// that an unset deployment is byte-for-byte the legacy unbounded behavior:
//
//   - a process-wide concurrency Semaphore that caps how many mutations may hold
//     a pool connection at once (acquired BEFORE BeginTx so the cap bounds conns
//     held, not conns waited on), and
//   - a per-operator-token sliding-window RateLimiter (modeled on
//     internal/loginthrottle/limiter.go: MaxKeys fail-closed + an injected Now
//     for deterministic tests) so one operator token cannot drive the whole
//     mutating budget.
//
// Both are in-memory single-process structures; a multi-replica deployment would
// layer a central limiter on top (follow-up), exactly as loginthrottle notes for
// its own IP buckets.
package mutateguard

import (
	"context"
	"errors"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
)

// ErrBusy is returned by Semaphore.Acquire when the mutating concurrency cap is
// saturated and the bounded acquire window elapsed before a slot freed. It is a
// clean "try again" signal (mapped to HTTP 429 upstream), never a hang.
var ErrBusy = errors.New("mutateguard: mutating concurrency saturated")

// Semaphore caps concurrent mutating executions process-wide. A nil or
// zero/negative-size Semaphore is the DISABLED sentinel: Acquire is an immediate
// no-op and Release does nothing, reproducing the legacy unbounded behavior.
type Semaphore struct {
	sem    *semaphore.Weighted
	enable bool
}

// NewSemaphore builds a concurrency cap of the given size. size <= 0 disables it
// (unbounded / legacy) — the caller passes the parsed knob default unchanged.
func NewSemaphore(size int) *Semaphore {
	if size <= 0 {
		return &Semaphore{enable: false}
	}
	return &Semaphore{sem: semaphore.NewWeighted(int64(size)), enable: true}
}

// Acquire reserves one slot, waiting at most acquireWait for one to free. On
// success it returns a release func the caller MUST defer. When the guard is
// disabled it returns a no-op release immediately. On timeout it returns ErrBusy
// (never blocks past acquireWait). A negative acquireWait is treated as the
// caller's parent ctx deadline only (no extra bound).
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
		// Acquire only fails on ctx cancel/deadline — surface a clean busy signal
		// rather than the raw context error so the handler maps it to 429.
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

// Enabled reports whether the concurrency cap is active (size > 0).
func (s *Semaphore) Enabled() bool { return s != nil && s.enable }

// RateLimiter is a per-key sliding-window counter. A nil or non-positive-limit
// RateLimiter is the DISABLED sentinel: Allow always returns true. It is keyed
// on the operator token id so the budget is per operator, not per tenant.
type RateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	maxKeys int
	now     func() time.Time
	hits    map[string][]time.Time
	enable  bool
}

// NewRateLimiter builds a sliding-window limiter of `limit` events per `window`
// per key. limit <= 0 disables it (legacy / unbounded). maxKeys caps tracked
// keys (fail-closed beyond it, mirroring loginthrottle.MaxKeys); <= 0 falls back
// to a sane default. now defaults to time.Now when nil (tests inject a clock).
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

// Allow records one event for key and reports whether it was within budget. When
// over budget it does NOT record the event (so a rejected attempt does not push
// the window further out) and returns a coarse RetryAfter. A disabled limiter
// always allows. fail-closed: if the tracked-key table is full and the key is
// new, the request is denied to protect memory (same posture as loginthrottle).
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
			// Memory protection: refuse a new key rather than grow unbounded.
			return false, l.window
		}
	}
	kept := pruneLocked(stamps, cutoff)
	if len(kept) >= l.limit {
		// Over budget: keep the pruned slice (don't record) so a rejection never
		// extends the window, and surface a coarse retry hint.
		l.hits[key] = kept
		return false, l.window
	}
	kept = append(kept, now)
	l.hits[key] = kept
	return true, 0
}

// Enabled reports whether the limiter is active (limit > 0).
func (l *RateLimiter) Enabled() bool { return l != nil && l.enable }

// pruneLocked returns the timestamps newer than cutoff, reusing the backing array.
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

// evictLocked drops keys whose every timestamp aged past the window, freeing room
// for a new key. Returns true if at least one key was reclaimed. Called with the
// mutex held.
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
