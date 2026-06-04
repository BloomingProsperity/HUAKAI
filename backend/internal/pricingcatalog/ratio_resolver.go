package pricingcatalog

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

const defaultRatioResolverTTL = 30 * time.Second

var defaultGroupRatio = decimal.NewFromInt(1)

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

func (r *RatioResolver) Resolve(ctx context.Context, tenantID, poolGroupID int64) decimal.Decimal {
	if r == nil || r.store == nil || tenantID <= 0 || poolGroupID <= 0 {
		return defaultGroupRatio
	}
	key := ratioCacheKey{tenantID: tenantID, poolGroupID: poolGroupID}
	now := time.Now()
	if ratio, ok := r.cached(key, now); ok {
		return ratio
	}

	row, err := r.store.GetRatio(ctx, tenantID, poolGroupID)
	if err == nil && row.Ratio.IsPositive() {
		r.put(key, row.Ratio, now)
		return row.Ratio
	}
	if errors.Is(err, ErrNotFound) {
		r.put(key, defaultGroupRatio, now)
		return defaultGroupRatio
	}
	if ratio, ok := r.lastKnownGood(key); ok {
		return ratio
	}
	return defaultGroupRatio
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

func (r *RatioResolver) lastKnownGood(key ratioCacheKey) (decimal.Decimal, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.cache[key]
	if !ok || !entry.ratio.IsPositive() {
		return decimal.Zero, false
	}
	return entry.ratio, true
}
