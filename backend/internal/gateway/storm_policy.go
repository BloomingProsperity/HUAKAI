// A07.3: 3-scope refresh storm policy compositor.
// Spec: docs/specs/upstream-credential-management.md §A07 / synthesis §1 A07.
//
// Composes A07.1 (TokenBucket) and A07.2 (SingleFlight) into the three-scope
// refresh-storm controller:
//
//   scope 1 (account):  SingleFlight dedup — N concurrent same-account callers
//                       result in exactly ONE inner execution; followers share
//                       the leader's result without consuming additional budget.
//                       This is the critical OAuth-storm prevention: 100 same-
//                       account 401s ⇒ 1 vendor refresh call.
//
//   scope 2 (endpoint): per-(provider, oauth_url) TokenBucket — protects vendor
//                       endpoint from M-account simultaneous-expiry stampede.
//
//   scope 3 (global):   process-wide TokenBucket — last-resort cap.
//
// Acquire order: account-singleflight WRAPS endpoint→global→fn so followers
// pay no budget. Refund is fired only when a bucket denied (NOT on fn error)
// so a failed vendor call still consumes its budget — refunding-on-failure
// would re-open the storm window.
//
// No IO, no network, no credential contact: pure composition + admission.
// Synthesis of two parallel-draft lanes (CLAUDE.md #10 + 2026-05-04 directive).
package gateway

import (
	"sync"
	"time"
)

// DenyReason identifies which budget scope (if any) rejected an Acquire call.
// DenyAccount is intentionally absent: the SingleFlight scope deduplicates,
// it never denies — followers receive the leader's outcome verbatim.
type DenyReason string

const (
	DenyNone     DenyReason = ""
	DenyEndpoint DenyReason = "endpoint"
	DenyGlobal   DenyReason = "global"
)

// StormPolicyConfig holds the rate/burst parameters for both bucket scopes.
// AccountSF may be injected to share account-dedup state across multiple
// StormPolicy instances; nil means each StormPolicy owns a private one.
type StormPolicyConfig struct {
	GlobalRate       float64 // tokens/second for the shared global bucket
	GlobalBurst      float64 // capacity ceiling for the global bucket
	PerEndpointRate  float64 // tokens/second for each per-endpoint bucket
	PerEndpointBurst float64 // capacity ceiling for each per-endpoint bucket
	AccountSF        *SingleFlight
}

// StormPolicy is the three-scope refresh storm controller. All methods are
// safe for concurrent use.
type StormPolicy struct {
	cfg             StormPolicyConfig
	globalBucket    *TokenBucket
	endpointBuckets sync.Map // map[string]*TokenBucket — lazy on first Acquire
	accountSF       *SingleFlight
}

// stormPolicyResult is what the singleflight inner executor returns. The outer
// Acquire unpacks this back into (val, err, denied).
type stormPolicyResult struct {
	val    any
	denied DenyReason
}

// NewStormPolicy returns an initialized policy. Rate/burst values are taken
// literally — 0 means "never admit", positive values mean their literal token
// budget. Callers are responsible for passing meaningful defaults; this
// primitive does not silently inject "permissive" sentinels (that would mask
// a misconfigured operator policy as "all-allow").
//
// AccountSF may be supplied to share dedup state across StormPolicy instances;
// nil means each policy owns a private SingleFlight.
func NewStormPolicy(cfg StormPolicyConfig) *StormPolicy {
	accountSF := cfg.AccountSF
	if accountSF == nil {
		accountSF = NewSingleFlight()
	}
	cfg.AccountSF = accountSF
	return &StormPolicy{
		cfg:          cfg,
		globalBucket: NewTokenBucket(cfg.GlobalRate, cfg.GlobalBurst),
		accountSF:    accountSF,
	}
}

// endpointBucket returns the TokenBucket for endpointKey, lazy-creating one on
// first access (race-safe via sync.Map LoadOrStore).
func (p *StormPolicy) endpointBucket(endpointKey string) *TokenBucket {
	if v, ok := p.endpointBuckets.Load(endpointKey); ok {
		return v.(*TokenBucket)
	}
	nb := NewTokenBucket(p.cfg.PerEndpointRate, p.cfg.PerEndpointBurst)
	actual, _ := p.endpointBuckets.LoadOrStore(endpointKey, nb)
	return actual.(*TokenBucket)
}

// Acquire enforces the three-scope policy and runs fn at most once per
// concurrent same-account caller-set.
//
// Returns:
//   - (val, fn-err, DenyNone)        — fn executed (or its result was shared by a follower)
//   - (nil, nil, DenyEndpoint)       — endpoint bucket exhausted; no fn run
//   - (nil, nil, DenyGlobal)         — global bucket exhausted; no fn run
//
// Refund is fired only on bucket denial. fn errors keep tokens consumed so a
// failed attempt does not reopen the storm window.
func (p *StormPolicy) Acquire(
	now time.Time,
	accountID, endpointKey string,
	fn func() (any, error),
) (val any, err error, denied DenyReason) {
	eb := p.endpointBucket(endpointKey)

	wrapped, fnErr, _ := p.accountSF.Do(accountID, func() (any, error) {
		// Scope 2: endpoint
		if !eb.TryAcquire(now) {
			return stormPolicyResult{denied: DenyEndpoint}, nil
		}
		// Scope 3: global — refund endpoint on global denial
		if !p.globalBucket.TryAcquire(now) {
			eb.Refund(now)
			return stormPolicyResult{denied: DenyGlobal}, nil
		}
		// Both buckets admitted; run fn. If fn errors, tokens stay consumed
		// (failed attempts must NOT reopen storm window).
		v, e := fn()
		return stormPolicyResult{val: v, denied: DenyNone}, e
	})

	result, ok := wrapped.(stormPolicyResult)
	if !ok {
		// Defensive: fn ran outside our wrapper somehow; surface raw value.
		return wrapped, fnErr, DenyNone
	}
	return result.val, fnErr, result.denied
}

// NextEligibleAt returns the earliest wall-clock time at which an Acquire for
// endpointKey could succeed. Schedulers use this to choose between waiting and
// failing over to another endpoint.
//
// Returns max(global.NextAvailableAt, endpoint.NextAvailableAt). If either
// bucket reports the zero time ("never"), zero is propagated so callers can
// detect the unsatisfiable case.
func (p *StormPolicy) NextEligibleAt(now time.Time, endpointKey string) time.Time {
	eb := p.endpointBucket(endpointKey)
	globalNext := p.globalBucket.NextAvailableAt(now)
	endpointNext := eb.NextAvailableAt(now)
	if globalNext.IsZero() || endpointNext.IsZero() {
		return time.Time{}
	}
	if globalNext.After(endpointNext) {
		return globalNext
	}
	return endpointNext
}
