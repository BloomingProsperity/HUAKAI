package notify

import (
	"sync"
	"time"
)

// DefaultNotifyTypeCacheTTL bounds how long a cached notify_type may gate the
// cheap in-process short-circuit before NotifyLowBalance re-reads the database.
// A user who switches from "none" to an active channel sees delivery resume
// within at most this window; "none" is by far the common steady state, so the
// cache keeps that case off the shared DB pool on the settlement hot path.
const DefaultNotifyTypeCacheTTL = 30 * time.Second

// notifyTypeCache memoises the last observed notify_type per (tenant,user) so
// the settlement hot path can skip the GetSettings DB read when notifications
// are disabled. It never caches delivery payloads or secrets — only the cheap
// routing discriminator — so an active channel always falls through to a fresh
// DB read and full validation.
type notifyTypeCache struct {
	ttl time.Duration
	mu  sync.Mutex
	m   map[notifyTypeCacheKey]notifyTypeCacheEntry
}

type notifyTypeCacheKey struct {
	tenantID int64
	userID   int64
}

type notifyTypeCacheEntry struct {
	notifyType Type
	storedAt   time.Time
}

func newNotifyTypeCache(ttl time.Duration) *notifyTypeCache {
	if ttl <= 0 {
		return nil
	}
	return &notifyTypeCache{
		ttl: ttl,
		m:   make(map[notifyTypeCacheKey]notifyTypeCacheEntry),
	}
}

// disabled reports whether a fresh cache entry says notifications are off for
// this (tenant,user). Only a fresh TypeNone entry returns true; everything else
// (miss, stale, or an active channel) returns false so the caller reads the DB.
func (c *notifyTypeCache) disabled(tenantID, userID int64, now time.Time) bool {
	if c == nil {
		return false
	}
	key := notifyTypeCacheKey{tenantID: tenantID, userID: userID}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.m[key]
	if !ok {
		return false
	}
	if now.Sub(entry.storedAt) >= c.ttl {
		delete(c.m, key)
		return false
	}
	return entry.notifyType == TypeNone
}

// store records the freshly observed notify_type for this (tenant,user).
func (c *notifyTypeCache) store(tenantID, userID int64, notifyType Type, now time.Time) {
	if c == nil {
		return
	}
	key := notifyTypeCacheKey{tenantID: tenantID, userID: userID}
	c.mu.Lock()
	c.m[key] = notifyTypeCacheEntry{notifyType: notifyType, storedAt: now}
	c.mu.Unlock()
}
