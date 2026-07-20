package usageanalyticshttp

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetOrLoadReturnsHitWithinTTLWithoutCallingLoaderAgain(t *testing.T) {
	var calls int32
	loader := func() (any, error) {
		return atomic.AddInt32(&calls, 1), nil
	}

	first, hit, err := GetOrLoad("hit-within-ttl", time.Minute, loader)
	if err != nil || hit || first.(int32) != 1 {
		t.Fatalf("first value=%v hit=%v err=%v want value=1 hit=false", first, hit, err)
	}
	second, hit, err := GetOrLoad("hit-within-ttl", time.Minute, loader)
	if err != nil || !hit || second.(int32) != 1 {
		t.Fatalf("second value=%v hit=%v err=%v want cached value=1 hit=true", second, hit, err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("loader calls=%d want 1; mutation without cache hit path calls loader again", got)
	}
}

func TestGetOrLoadExpiresEntriesAfterTTL(t *testing.T) {
	var calls int32
	loader := func() (any, error) {
		return atomic.AddInt32(&calls, 1), nil
	}

	if _, _, err := GetOrLoad("expires-after-ttl", 15*time.Millisecond, loader); err != nil {
		t.Fatalf("initial load err=%v", err)
	}
	time.Sleep(25 * time.Millisecond)
	value, hit, err := GetOrLoad("expires-after-ttl", time.Minute, loader)
	if err != nil || hit || value.(int32) != 2 {
		t.Fatalf("after expiry value=%v hit=%v err=%v want recomputed value=2 hit=false", value, hit, err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("loader calls=%d want 2; mutation ignoring TTL expiry keeps stale value", got)
	}
}

func TestGetOrLoadCoalescesConcurrentMissesForSameKey(t *testing.T) {
	const workers = 50
	var calls int32
	started := make(chan struct{})
	release := make(chan struct{})
	loader := func() (any, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			close(started)
		}
		<-release
		return "shared", nil
	}

	var wg sync.WaitGroup
	values := make(chan any, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, _, err := GetOrLoad("same-key-concurrent-miss", time.Minute, loader)
			if err != nil {
				t.Errorf("GetOrLoad err=%v", err)
				return
			}
			values <- value
		}()
	}
	<-started
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	close(values)

	for value := range values {
		if value.(string) != "shared" {
			t.Fatalf("value=%v want shared", value)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("loader calls=%d want 1; mutation deleting inflight coalescing stampedes loaders", got)
	}
}

func TestGetOrLoadDoesNotCacheLoaderErrors(t *testing.T) {
	var calls int32
	backendErr := errors.New("backend unavailable")
	loader := func() (any, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return nil, backendErr
		}
		return "recovered", nil
	}

	value, hit, err := GetOrLoad("error-is-not-cached", time.Minute, loader)
	if !errors.Is(err, backendErr) || hit || value != nil {
		t.Fatalf("first value=%v hit=%v err=%v want backend error without cache hit", value, hit, err)
	}
	value, hit, err = GetOrLoad("error-is-not-cached", time.Minute, loader)
	if err != nil || hit || value.(string) != "recovered" {
		t.Fatalf("retry value=%v hit=%v err=%v want fresh successful load", value, hit, err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("loader calls=%d want 2; mutation caching errors skips retry", got)
	}
}

func TestGetOrLoadReleasesInflightAfterLoaderPanic(t *testing.T) {
	key := "panic-releases-inflight"
	_, hit, err := GetOrLoad(key, time.Minute, func() (any, error) {
		panic("测试崩溃")
	})
	if hit || !errors.Is(err, errLoaderPanic) {
		t.Fatalf("panic load hit=%v err=%v，期望返回可识别错误", hit, err)
	}

	value, hit, err := GetOrLoad(key, time.Minute, func() (any, error) {
		return "recovered", nil
	})
	if err != nil || hit || value != "recovered" {
		t.Fatalf("panic 后重试 value=%v hit=%v err=%v", value, hit, err)
	}
}

func TestSnapshotCacheEvictsExpiredAndBoundsLiveEntries(t *testing.T) {
	state.mu.Lock()
	previousEntries := state.entries
	previousInflight := state.inflight
	state.entries = map[string]cacheEntry{
		"expired": {value: 1, expiresAt: time.Now().Add(-time.Second)},
	}
	state.inflight = map[string]*inflightCall{}
	for i := 0; i < maxSnapshotCacheEntries; i++ {
		key := fmt.Sprintf("live-%d", i)
		state.entries[key] = cacheEntry{value: i, expiresAt: time.Now().Add(time.Hour)}
	}
	state.mu.Unlock()
	t.Cleanup(func() {
		state.mu.Lock()
		state.entries = previousEntries
		state.inflight = previousInflight
		state.mu.Unlock()
	})

	if _, _, err := GetOrLoad("incoming", time.Hour, func() (any, error) { return 1, nil }); err != nil {
		t.Fatalf("GetOrLoad: %v", err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if _, ok := state.entries["expired"]; ok {
		t.Fatal("插入新快照时应清理全部过期项")
	}
	if len(state.entries) > maxSnapshotCacheEntries {
		t.Fatalf("缓存条目=%d，超过上限 %d", len(state.entries), maxSnapshotCacheEntries)
	}
	if _, ok := state.entries["incoming"]; !ok {
		t.Fatal("新加载项未进入缓存")
	}
}
