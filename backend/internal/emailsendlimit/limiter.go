// HUAKAI · iKun

// Package emailsendlimit provides a process-local per-IP limiter for auth email send
// routes. It is the IP layer in front of the existing per-email sender cooldown.
package emailsendlimit

import (
	"strings"
	"sync"
	"time"
)

// Config controls the rolling-window limiter. Zero values fall back to DefaultConfig.
type Config struct {
	Window  time.Duration
	Limit   int
	MaxKeys int
	Now     func() time.Time
}

// DefaultConfig is intentionally wider than the per-email cooldown so normal users
// behind shared NATs are not blocked by ordinary resend behavior.
func DefaultConfig() Config {
	return Config{
		Window:  time.Minute,
		Limit:   20,
		MaxKeys: 100000,
		Now:     time.Now,
	}
}

type bucket struct {
	hits     []time.Time
	lastSeen time.Time
}

// Limiter is a concurrency-safe in-memory rolling-window limiter. It has single-process
// semantics; multi-replica deployments need a centralized implementation as a follow-up.
type Limiter struct {
	mu      sync.Mutex
	cfg     Config
	now     func() time.Time
	buckets map[string]*bucket
}

// New builds a limiter from cfg, replacing zero values with DefaultConfig.
func New(cfg Config) *Limiter {
	d := DefaultConfig()
	if cfg.Window <= 0 {
		cfg.Window = d.Window
	}
	if cfg.Limit <= 0 {
		cfg.Limit = d.Limit
	}
	if cfg.MaxKeys <= 0 {
		cfg.MaxKeys = d.MaxKeys
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Limiter{cfg: cfg, now: cfg.Now, buckets: make(map[string]*bucket)}
}

// Allow records one auth email send attempt for clientIP. It returns false with a
// coarse Retry-After when the IP has exhausted its configured rolling window.
func (l *Limiter) Allow(clientIP string) (bool, time.Duration) {
	if l == nil {
		return true, 0
	}
	key := strings.TrimSpace(clientIP)
	if key == "" {
		key = "unknown"
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) >= l.cfg.MaxKeys && !l.evictLocked(now) {
			return false, time.Second
		}
		b = &bucket{}
		l.buckets[key] = b
	}
	b.lastSeen = now
	l.pruneBucketLocked(b, now)

	if len(b.hits) >= l.cfg.Limit {
		return false, ceilSeconds(b.hits[0].Add(l.cfg.Window).Sub(now))
	}

	b.hits = append(b.hits, now)
	return true, 0
}

func (l *Limiter) pruneBucketLocked(b *bucket, now time.Time) {
	cutoff := now.Add(-l.cfg.Window)
	if len(b.hits) == 0 {
		return
	}
	kept := b.hits[:0]
	for _, t := range b.hits {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	b.hits = kept
}

func (l *Limiter) evictLocked(now time.Time) bool {
	cutoff := now.Add(-l.cfg.Window)
	evicted := false
	for k, b := range l.buckets {
		if b.lastSeen.Before(cutoff) {
			delete(l.buckets, k)
			evicted = true
		}
	}
	return evicted
}

func ceilSeconds(d time.Duration) time.Duration {
	if d <= 0 {
		return time.Second
	}
	return ((d + time.Second - 1) / time.Second) * time.Second
}
