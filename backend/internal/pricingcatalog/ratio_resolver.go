package pricingcatalog

import (
	"context"
	"errors"
	"expvar"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

const defaultRatioResolverTTL = 30 * time.Second

var defaultGroupRatio = decimal.NewFromInt(1)

const (
	ratioResolverBackendErrorTotal = "backend_error_without_lkg_total"
	ratioResolverStaleTotal        = "stale_on_backend_error_total"
)

var ratioResolverSignals = expvar.NewMap("pricing_ratio_resolver")

func init() {
	ratioResolverSignals.Add(ratioResolverBackendErrorTotal, 0)
	ratioResolverSignals.Add(ratioResolverStaleTotal, 0)
}

type RatioResolver struct {
	store Store
	ttl   time.Duration

	mu    sync.Mutex
	cache map[ratioCacheKey]ratioCacheEntry
}

type ratioCacheKey struct {
	tenantID    int64
	poolGroupID int64
}

type ratioCacheEntry struct {
	ratio     decimal.Decimal
	expiresAt time.Time
}

func NewRatioResolver(store Store, ttl time.Duration) *RatioResolver {
	if ttl <= 0 {
		ttl = defaultRatioResolverTTL
	}
	return &RatioResolver{
		store: store,
		ttl:   ttl,
		cache: make(map[ratioCacheKey]ratioCacheEntry),
	}
}

func (r *RatioResolver) Resolve(ctx context.Context, tenantID, poolGroupID int64) (decimal.Decimal, error) {
	ratio, _, err := r.ResolveWithSignal(ctx, tenantID, poolGroupID)
	return ratio, err
}

func (r *RatioResolver) ResolveWithSignal(ctx context.Context, tenantID, poolGroupID int64) (decimal.Decimal, bool, error) {
	if r == nil || r.store == nil || tenantID <= 0 || poolGroupID <= 0 {
		return defaultGroupRatio, false, nil
	}
	key := ratioCacheKey{tenantID: tenantID, poolGroupID: poolGroupID}
	now := time.Now()
	if ratio, ok := r.cached(key, now); ok {
		return ratio, false, nil
	}

	row, err := r.store.GetRatio(ctx, tenantID, poolGroupID)
	if err == nil && row.Ratio.IsPositive() {
		r.put(key, row.Ratio, now)
		return row.Ratio, false, nil
	}
	if err == nil {
		err = fmt.Errorf("%w: non-positive pricing ratio for tenant %d pool group %d", ErrBackend, tenantID, poolGroupID)
	}
	if errors.Is(err, ErrNotFound) {
		r.put(key, defaultGroupRatio, now)
		return defaultGroupRatio, false, nil
	}
	if ratio, ok := r.lastKnownGood(key); ok {
		ratioResolverSignals.Add(ratioResolverStaleTotal, 1)
		slog.WarnContext(ctx, "pricing ratio resolver served stale ratio after backend error",
			"tenant_id", tenantID,
			"pool_group_id", poolGroupID,
			"error", err,
		)
		return ratio, false, nil
	}
	ratioResolverSignals.Add(ratioResolverBackendErrorTotal, 1)
	slog.ErrorContext(ctx, "pricing ratio resolver served default ratio after backend error without last-known-good",
		"tenant_id", tenantID,
		"pool_group_id", poolGroupID,
		"default_group_ratio", defaultGroupRatio.String(),
		"error", err,
	)
	// 可用性优先: 冷启且后端抖动时按 1.0 放行,同时把本次请求标记为待对账。
	return defaultGroupRatio, true, nil
}

func (r *RatioResolver) cached(key ratioCacheKey, now time.Time) (decimal.Decimal, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.cache[key]
	if !ok || now.After(entry.expiresAt) {
		return decimal.Zero, false
	}
	return entry.ratio, true
}

func (r *RatioResolver) put(key ratioCacheKey, ratio decimal.Decimal, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[key] = ratioCacheEntry{
		ratio:     ratio,
		expiresAt: now.Add(r.ttl),
	}
}

func (r *RatioResolver) Invalidate(tenantID, poolGroupID int64) {
	if r == nil || tenantID <= 0 || poolGroupID <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cache, ratioCacheKey{tenantID: tenantID, poolGroupID: poolGroupID})
}

func (r *RatioResolver) lastKnownGood(key ratioCacheKey) (decimal.Decimal, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.cache[key]
	if !ok || !entry.ratio.IsPositive() {
		return decimal.Zero, false
	}
	return entry.ratio, true
}

type RatioResolverSignalSnapshot struct {
	BackendErrorWithoutLKGTotal int64
	StaleOnBackendErrorTotal    int64
}

func SnapshotRatioResolverSignals() RatioResolverSignalSnapshot {
	return RatioResolverSignalSnapshot{
		BackendErrorWithoutLKGTotal: ratioResolverSignalValue(ratioResolverBackendErrorTotal),
		StaleOnBackendErrorTotal:    ratioResolverSignalValue(ratioResolverStaleTotal),
	}
}

func ratioResolverSignalValue(key string) int64 {
	v, ok := ratioResolverSignals.Get(key).(*expvar.Int)
	if !ok {
		return 0
	}
	return v.Value()
}
