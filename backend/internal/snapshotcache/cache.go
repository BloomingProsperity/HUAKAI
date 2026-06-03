package snapshotcache

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

// GetOrLoad returns a non-expired cached value for key, or runs loader once for
// concurrent cache misses on the same key. Loader errors are returned but never
// cached, so later calls can retry the backend.
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
