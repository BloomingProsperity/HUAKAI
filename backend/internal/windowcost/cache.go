// Package windowcost implements SUB2-EGRESS-03: per-account 5-hour session
// window spend cap. A background Worker aggregates actual_cost from
// usage_records into an in-memory Cache; a pool gate reads the cache on the
// hot selection path with no SQL.
//
// Safety design:
//   - Opt-in via window_cost_limit_cents > 0 on provider_accounts.
//   - Fail-open: missing/stale cache entry or limit<=0 → account stays eligible.
//   - A bug can only make the cap less effective, never wrongly bench a healthy account.
package windowcost

import (
	"sync"
	"time"
)

// staleness threshold: a cache entry older than this is considered stale and
// triggers fail-open (account stays eligible) until the worker refreshes it.
const staleDuration = 3 * time.Minute

// entry holds an aggregated window cost for one account.
type entry struct {
	cents     int64
	updatedAt time.Time
}

// Cache is a thread-safe in-memory store of per-account window costs.
// The zero value is usable (empty cache → fail-open for all accounts).
type Cache struct {
	mu      sync.RWMutex
	entries map[int64]entry
}

// NewCache constructs an empty Cache.
func NewCache() *Cache {
	return &Cache{entries: make(map[int64]entry)}
}

// Set stores the aggregated cost for an account.
func (c *Cache) Set(accountID, cents int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[int64]entry)
	}
	c.entries[accountID] = entry{cents: cents, updatedAt: time.Now()}
}

// CurrentCost returns (cents, fresh). fresh is false when the entry is absent
// or older than staleDuration; callers must treat fresh=false as fail-open.
func (c *Cache) CurrentCost(accountID int64) (cents int64, fresh bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[accountID]
	if !ok {
		return 0, false
	}
	if time.Since(e.updatedAt) > staleDuration {
		return 0, false
	}
	return e.cents, true
}

// CostReader is the read-only interface the gate uses; allows nil injection
// for fail-open default.
type CostReader interface {
	CurrentCost(accountID int64) (cents int64, fresh bool)
}
