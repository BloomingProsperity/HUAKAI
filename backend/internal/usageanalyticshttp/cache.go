package usageanalyticshttp

import (
	"errors"
	"sync"
	"time"
)

var errNilLoader = errors.New("snapshotcache: loader is nil")

// errLoaderPanic 在 loader() panic 后交给并发等待同 key 的请求,避免它们把零值当成功。
var errLoaderPanic = errors.New("snapshotcache: loader panicked")

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

	var (
		value    any
		err      error
		panicked = true // 假定 loader panic,直到它正常返回后置 false
	)
	// defer 保证即使 loader() panic,也会清理 inflight 并唤醒等待者:否则该 key 的 inflight
	// 条目永久孤立、WaitGroup 卡在 1,每个后续同 key 请求都会永远阻塞在 call.wg.Wait()
	// (死锁 + goroutine 泄漏)。net/http 只 recover 触发 panic 的那一个请求。
	defer func() {
		state.mu.Lock()
		if !panicked && err == nil && ttl > 0 {
			state.entries[key] = cacheEntry{value: value, expiresAt: time.Now().Add(ttl)}
		}
		delete(state.inflight, key)
		state.mu.Unlock()

		call.value = value
		call.err = err
		if panicked && call.err == nil {
			// 让并发等待同 key 的其它请求收到错误,而不是把零值当成功结果返回。
			call.err = errLoaderPanic
		}
		call.wg.Done()
	}()

	value, err = loader()
	panicked = false

	return value, false, err
}
