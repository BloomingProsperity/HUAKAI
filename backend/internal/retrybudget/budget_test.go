package retrybudget

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBudgetAllowDisabledNeverConsumes(t *testing.T) {
	now := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	budget := New(0, time.Minute, WithClock(func() time.Time { return now }))

	for i := 0; i < 10; i++ {
		if !budget.Allow(7) {
			t.Fatalf("disabled budget denied retry %d; budget=0 must preserve old unlimited behavior", i+1)
		}
	}
}

func TestBudgetAllowScopesByTenantAndSlidingWindow(t *testing.T) {
	now := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	budget := New(2, time.Minute, WithClock(func() time.Time { return now }))

	// 两次独立的 budget.Allow(7) 调用各自消费配额(有副作用),分别捕获再判,避免 SA4000 把它误判为自反比较。
	allow1 := budget.Allow(7)
	allow2 := budget.Allow(7)
	if !allow1 || !allow2 {
		t.Fatal("tenant 7 first two retries should be allowed")
	}
	if budget.Allow(7) {
		t.Fatal("tenant 7 third retry should be denied inside the same window")
	}
	if !budget.Allow(8) {
		t.Fatal("tenant 8 must not inherit tenant 7 retry usage")
	}

	now = now.Add(time.Minute + time.Nanosecond)
	if !budget.Allow(7) {
		t.Fatal("tenant 7 retry should be allowed after the sliding window expires")
	}
}

func TestBudgetAllowConcurrentCallsDoNotExceedLimit(t *testing.T) {
	budget := New(5, time.Minute)
	var allowed int64
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if budget.Allow(7) {
				atomic.AddInt64(&allowed, 1)
			}
		}()
	}
	wg.Wait()

	if allowed != 5 {
		t.Fatalf("allowed=%d want exactly 5; mutex must make the cap atomic", allowed)
	}
}
