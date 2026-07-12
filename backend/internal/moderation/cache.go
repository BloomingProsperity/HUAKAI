package moderation

import (
	"container/list"
	"sync"
	"time"
)

type CacheOptions struct {
	MaxEntries int
	TTL        time.Duration
	Now        func() time.Time
}

type ttlLRU[K comparable, V any] struct {
	mu         sync.Mutex
	maxEntries int
	ttl        time.Duration
	now        func() time.Time
	ll         *list.List
	items      map[K]*list.Element
}

type cacheEntry[K comparable, V any] struct {
	key       K
	value     V
	expiresAt time.Time
}

func newTTLLRU[K comparable, V any](opts CacheOptions) *ttlLRU[K, V] {
	maxEntries := opts.MaxEntries
	if maxEntries <= 0 {
		maxEntries = 256
	}
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &ttlLRU[K, V]{
		maxEntries: maxEntries,
		ttl:        ttl,
		now:        now,
		ll:         list.New(),
		items:      make(map[K]*list.Element),
	}
}

func (c *ttlLRU[K, V]) Get(key K) (V, bool) {
	value, fresh, _ := c.get(key, false)
	return value, fresh
}

func (c *ttlLRU[K, V]) GetAllowStale(key K) (V, bool, bool) {
	return c.get(key, true)
}

func (c *ttlLRU[K, V]) get(key K, allowStale bool) (V, bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var zero V
	el, ok := c.items[key]
	if !ok {
		return zero, false, false
	}
	entry := el.Value.(cacheEntry[K, V])
	if !entry.expiresAt.After(c.now()) {
		if allowStale {
			return entry.value, false, true
		}
		c.ll.Remove(el)
		delete(c.items, key)
		return zero, false, false
	}
	c.ll.MoveToFront(el)
	return entry.value, true, false
}

func (c *ttlLRU[K, V]) Set(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := cacheEntry[K, V]{
		key:       key,
		value:     value,
		expiresAt: c.now().Add(c.ttl),
	}
	if el, ok := c.items[key]; ok {
		el.Value = entry
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(entry)
	c.items[key] = el
	for c.ll.Len() > c.maxEntries {
		oldest := c.ll.Back()
		if oldest == nil {
			return
		}
		c.ll.Remove(oldest)
		delete(c.items, oldest.Value.(cacheEntry[K, V]).key)
	}
}
