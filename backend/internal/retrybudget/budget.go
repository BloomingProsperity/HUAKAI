package retrybudget

import (
	"sync"
	"time"
)

const defaultWindow = time.Minute

type Option func(*Budget)

type Budget struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	now    func() time.Time
	events map[int64][]time.Time
}

func New(limit int, window time.Duration, opts ...Option) *Budget {
	if window <= 0 {
		window = defaultWindow
	}
	b := &Budget{
		limit:  limit,
		window: window,
		now:    func() time.Time { return time.Now().UTC() },
		events: make(map[int64][]time.Time),
	}
	for _, opt := range opts {
		opt(b)
	}
	if b.now == nil {
		b.now = func() time.Time { return time.Now().UTC() }
	}
	return b
}

func WithClock(clock func() time.Time) Option {
	return func(b *Budget) {
		if clock != nil {
			b.now = clock
		}
	}
}

func (b *Budget) Allow(tenantID int64) bool {
	if b == nil || b.limit <= 0 {
		return true
	}
	now := b.now().UTC()
	cutoff := now.Add(-b.window)

	b.mu.Lock()
	defer b.mu.Unlock()

	events := b.events[tenantID]
	kept := 0
	for _, ts := range events {
		if ts.After(cutoff) {
			events[kept] = ts
			kept++
		}
	}
	events = events[:kept]
	if len(events) >= b.limit {
		if len(events) == 0 {
			delete(b.events, tenantID)
		} else {
			b.events[tenantID] = events
		}
		return false
	}
	events = append(events, now)
	b.events[tenantID] = events
	return true
}
