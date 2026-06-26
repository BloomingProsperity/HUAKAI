package usageanalyticshttp

import (
	"errors"
	"sync"
	"time"
)

var errNilLoader = errors.New("snapshotcache: loader is nil")

type cacheEntry struct {
	value     any
	expiresAt time.Time
}

type inflightCall struct {
	wg    sync.WaitGroup
	value any
	err   error
}

var state = struct {
	mu       sync.Mutex
	entries  map[string]cacheEntry
	inflight map[string]*inflightCall
}{
	entries:  map[string]cacheEntry{},
	inflight: map[string]*inflightCall{},
}

// GetOrLoad 返回 key 对应的未过期缓存值;若同一个 key 上发生并发缓存未命中,
// 则只运行一次 loader。loader 的错误会被返回但绝不缓存,这样后续调用还能重试
// 后端。
func GetOrLoad(key string, ttl time.Duration, loader func() (any, error)) (any, bool, error) {
	if loader == nil {
		return nil, false, errNilLoader
	}

	now := time.Now()
	state.mu.Lock()
	if entry, ok := state.entries[key]; ok && now.Before(entry.expiresAt) {
		value := entry.value
		state.mu.Unlock()
		return value, true, nil
	}
	if call, ok := state.inflight[key]; ok {
		state.mu.Unlock()
		call.wg.Wait()
		return call.value, false, call.err
	}
	call := &inflightCall{}
	call.wg.Add(1)
	state.inflight[key] = call
	state.mu.Unlock()

	value, err := loader()

	state.mu.Lock()
	if err == nil && ttl > 0 {
		state.entries[key] = cacheEntry{value: value, expiresAt: time.Now().Add(ttl)}
	}
	delete(state.inflight, key)
	state.mu.Unlock()

	call.value = value
	call.err = err
	call.wg.Done()

	return value, false, err
}
