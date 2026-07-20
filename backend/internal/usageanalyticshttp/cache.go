package usageanalyticshttp

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var errNilLoader = errors.New("snapshotcache: loader is nil")
var errLoaderPanic = errors.New("snapshotcache: loader panic")

const maxSnapshotCacheEntries = 1024

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
	if entry, ok := state.entries[key]; ok {
		if now.Before(entry.expiresAt) {
			value := entry.value
			state.mu.Unlock()
			return value, true, nil
		}
		delete(state.entries, key)
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

	value, err := runLoader(loader)

	state.mu.Lock()
	if err == nil && ttl > 0 {
		now := time.Now()
		evictSnapshotEntriesLocked(now, key)
		state.entries[key] = cacheEntry{value: value, expiresAt: now.Add(ttl)}
	}
	delete(state.inflight, key)
	state.mu.Unlock()

	call.value = value
	call.err = err
	call.wg.Done()

	return value, false, err
}

func evictSnapshotEntriesLocked(now time.Time, incomingKey string) {
	for key, entry := range state.entries {
		if !now.Before(entry.expiresAt) {
			delete(state.entries, key)
		}
	}
	if _, replacing := state.entries[incomingKey]; replacing || len(state.entries) < maxSnapshotCacheEntries {
		return
	}
	var oldestKey string
	var oldestExpiry time.Time
	for key, entry := range state.entries {
		if oldestKey == "" || entry.expiresAt.Before(oldestExpiry) {
			oldestKey, oldestExpiry = key, entry.expiresAt
		}
	}
	if oldestKey != "" {
		delete(state.entries, oldestKey)
	}
}

func runLoader(loader func() (any, error)) (value any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			value = nil
			err = fmt.Errorf("%w: %T", errLoaderPanic, recovered)
		}
	}()
	return loader()
}
