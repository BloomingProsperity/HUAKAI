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

// stormPolicyResult 是 singleflight 内层执行器返回的载荷,外层 Acquire 会还原为
// (val, denied, err)。
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

// Acquire 执行三层刷新风暴策略,并保证同账号并发调用集最多只执行一次 fn。
//
// 返回:
//   - (val, DenyNone, fn-err): fn 已执行,或 follower 共享了 leader 结果。
//   - (nil, DenyEndpoint, nil): endpoint bucket 耗尽,不执行 fn。
//   - (nil, DenyGlobal, nil): global bucket 耗尽,不执行 fn。
//
// 只有 bucket 拒绝时才退还已消费 token。fn 自身失败仍保留 token 消耗,避免失败重试重新打开风暴窗口。
func (p *StormPolicy) Acquire(
	now time.Time,
	accountID, endpointKey string,
	fn func() (any, error),
) (val any, denied DenyReason, err error) {
	eb := p.endpointBucket(endpointKey)

	wrapped, fnErr, _ := p.accountSF.Do(accountID, func() (any, error) {
		// 第二层:endpoint bucket。
		if !eb.TryAcquire(now) {
			return stormPolicyResult{denied: DenyEndpoint}, nil
		}
		// 第三层:global bucket;global 拒绝时退还 endpoint token。
		if !p.globalBucket.TryAcquire(now) {
			eb.Refund(now)
			return stormPolicyResult{denied: DenyGlobal}, nil
		}
		// 两层 bucket 均放行后执行 fn;fn 失败仍保留 token 消耗。
		v, e := fn()
		return stormPolicyResult{val: v, denied: DenyNone}, e
	})

	result, ok := wrapped.(stormPolicyResult)
	if !ok {
		// 防御性兜底:若 singleflight 返回了包装外值,直接透出原始值。
		return wrapped, DenyNone, fnErr
	}
	return result.val, result.denied, fnErr
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
