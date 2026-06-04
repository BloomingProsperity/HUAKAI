package auth

import (
	"sync"
	"time"
)

// storm_scope.go is the in-memory endpoint + global rate layer of the
// three-scope refresh-storm controller.
//
// The account scope (the critical same-account thundering-herd guard) stays
// DB-durable in StormController.Acquire — durable and cross-replica. This file
// adds the two remaining scopes A07 promises:
//
//   - provider-endpoint: one token bucket per (provider, endpoint), so M accounts
//     expiring at once cannot stampede a single vendor OAuth token endpoint.
//   - global: one process-wide bucket, a last-resort cap.
//
// Both are process-local (per-replica). That is a deliberate trade-off for this
// slice: it requires no schema change, and the durable account budget already
// prevents the worst case. Cross-replica endpoint/global budgets are future work.
//
// These buckets are a layer-local primitive, intentionally NOT the request-path
// gateway.TokenBucket: the credential worker (and this auth layer it depends on)
// must not import the gateway request-path package, which is also frozen. The
// arithmetic is the standard refill-on-demand token bucket.

// StormScopeConfig sets the token rate (tokens/second) and burst (ceiling) for
// the per-endpoint and global scopes. A scope is OFF (admit-all) unless BOTH its
// rate is positive AND its burst is at least one whole token — the layer is an
// opt-in additive throttle, never a silent blocker, so a zero/partial config
// degrades to "account scope only" rather than denying every refresh.
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

// stormScopeLimiter holds the in-memory buckets. A nil *stormScopeLimiter admits
// everything (account-scope-only deployment).
type stormScopeLimiter struct {
	cfg             StormScopeConfig
	globalBucket    *scopeBucket
	endpointBuckets sync.Map // map[string]*scopeBucket — lazy, race-safe
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

// endpointBucket lazily creates the bucket for key, race-safe via LoadOrStore.
func (l *stormScopeLimiter) endpointBucket(key string) *scopeBucket {
	if v, ok := l.endpointBuckets.Load(key); ok {
		return v.(*scopeBucket)
	}
	nb := newScopeBucket(l.cfg.PerEndpointRate, l.cfg.PerEndpointBurst)
	actual, _ := l.endpointBuckets.LoadOrStore(key, nb)
	return actual.(*scopeBucket)
}

// scopeBucket is a refill-on-demand token bucket. rate is tokens/second, burst
// the ceiling. Safe for concurrent use. The zero last-refill time means the
// bucket has never been observed; the first call anchors it without granting a
// spurious refill, which keeps the type fully testable with an injected clock.
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
		// Backward or equal clock: do not refill, but hold the cursor so a
		// later forward tick measures elapsed time from here.
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

// tryAcquire consumes one token at now, returning true iff one was available.
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

// refund returns one token (clamped to burst). Used only on the deny-cascade
// (endpoint admitted, then global denied) so a denied attempt does not waste the
// endpoint budget. It is NOT called on a failed refresh — a failed attempt must
// keep its tokens consumed or it would reopen the storm window.
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
