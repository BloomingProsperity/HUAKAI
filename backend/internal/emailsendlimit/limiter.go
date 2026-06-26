// HUAKAI · iKun

// Package emailsendlimit 为认证邮件发送路由提供进程内的按 IP 限流器。
// 它是叠在现有「按邮箱发件人冷却」之前的 IP 层。
package emailsendlimit

import (
	"strings"
	"sync"
	"time"
)

// Config 控制滑动窗口限流器。零值会回退到 DefaultConfig。
type Config struct {
	Window  time.Duration
	Limit   int
	MaxKeys int
	Now     func() time.Time
}

// DefaultConfig 故意比按邮箱冷却放得更宽,这样共享 NAT 后面的正常用户
// 不会因普通的重发行为而被挡住。
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

// Limiter 是并发安全的内存滑动窗口限流器。它是单进程语义;
// 多副本部署后续需要一个集中式实现。
type Limiter struct {
	mu      sync.Mutex
	cfg     Config
	now     func() time.Time
	buckets map[string]*bucket
}

// New 根据 cfg 构造一个限流器,零值用 DefaultConfig 替换。
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

// Allow 为 clientIP 记录一次认证邮件发送尝试。当该 IP 用尽其配置的
// 滑动窗口配额时,返回 false 并附带一个粗粒度的 Retry-After。
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
