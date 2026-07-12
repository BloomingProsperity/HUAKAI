package notify

import (
	"fmt"
	"sync"
	"time"
)

type RateLimiter struct {
	mu        sync.Mutex
	window    time.Duration
	last      map[string]time.Time
	lastSweep time.Time
}

func NewRateLimiter(window time.Duration) *RateLimiter {
	return &RateLimiter{
		window: window,
		last:   make(map[string]time.Time),
	}
}

func (l *RateLimiter) Allow(tenantID, userID int64, eventType string, now time.Time) bool {
	if l == nil || l.window <= 0 {
		return true
	}
	key := rateLimitKey(tenantID, userID, eventType)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.evictExpiredLocked(now)
	if last, ok := l.last[key]; ok && now.Sub(last) < l.window {
		return false
	}
	l.last[key] = now
	return true
}

func (l *RateLimiter) Release(tenantID, userID int64, eventType string, reservedAt time.Time) {
	if l == nil || l.window <= 0 {
		return
	}
	key := rateLimitKey(tenantID, userID, eventType)
	l.mu.Lock()
	defer l.mu.Unlock()
	if last, ok := l.last[key]; ok && last.Equal(reservedAt) {
		delete(l.last, key)
	}
}

func (l *RateLimiter) evictExpiredLocked(now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	if !l.lastSweep.IsZero() && now.Sub(l.lastSweep) < l.window {
		return
	}
	for key, last := range l.last {
		if !last.IsZero() && now.Sub(last) >= l.window {
			delete(l.last, key)
		}
	}
	l.lastSweep = now
}

func rateLimitKey(tenantID, userID int64, eventType string) string {
	return fmt.Sprintf("%d:%d:%s", tenantID, userID, eventType)
}
