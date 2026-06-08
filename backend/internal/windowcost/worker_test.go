package windowcost

import (
	"context"
	"errors"
	"testing"
	"time"
)

// --- fakes ---

type fakeListLimited struct {
	accounts []AccountRecord
	err      error
}

func (f *fakeListLimited) ListLimitedAccounts(_ context.Context) ([]AccountRecord, error) {
	return f.accounts, f.err
}

type fakeAggregator struct {
	cents map[int64]int64
	err   error
}

func (f *fakeAggregator) SumWindowCost(_ context.Context, accountID int64, _ time.Time) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.cents[accountID], nil
}

// --- cache unit tests ---

func TestCache_FreshEntry(t *testing.T) {
	c := NewCache()
	c.Set(42, 500)
	cents, fresh := c.CurrentCost(42)
	if !fresh {
		t.Fatal("expected fresh=true for newly set entry")
	}
	if cents != 500 {
		t.Fatalf("expected 500 cents, got %d", cents)
	}
}

func TestCache_Miss(t *testing.T) {
	c := NewCache()
	_, fresh := c.CurrentCost(99)
	if fresh {
		t.Fatal("expected fresh=false for cache miss")
	}
}

func TestCache_Stale(t *testing.T) {
	c := NewCache()
	// Manually insert a stale entry by backdating.
	c.mu.Lock()
	c.entries[7] = entry{cents: 100, updatedAt: time.Now().Add(-staleDuration - time.Second)}
	c.mu.Unlock()

	_, fresh := c.CurrentCost(7)
	if fresh {
		t.Fatal("expected fresh=false for stale entry")
	}
}

// --- worker tick tests ---

func TestWorker_TickPopulatesCache(t *testing.T) {
	windowStart := time.Now().Add(-1 * time.Hour)
	lister := &fakeListLimited{accounts: []AccountRecord{
		{ID: 1, TenantID: 10, SessionWindow5hStart: windowStart},
	}}
	agg := &fakeAggregator{cents: map[int64]int64{1: 300}}
	cache := NewCache()
	w := NewWorker(lister, agg, cache, 0, nil)
	w.tick(context.Background())

	cents, fresh := cache.CurrentCost(1)
	if !fresh {
		t.Fatal("expected fresh=true after tick")
	}
	if cents != 300 {
		t.Fatalf("expected 300, got %d", cents)
	}
}

func TestWorker_TickListerError_FailOpen(t *testing.T) {
	lister := &fakeListLimited{err: errors.New("db down")}
	agg := &fakeAggregator{}
	cache := NewCache()
	cache.Set(1, 999) // pre-existing entry

	w := NewWorker(lister, agg, cache, 0, nil)
	w.tick(context.Background())

	// Cache must NOT be cleared; lister error → leave as-is (fail-open).
	cents, fresh := cache.CurrentCost(1)
	if !fresh {
		t.Fatal("expected cache untouched on lister error (fail-open)")
	}
	if cents != 999 {
		t.Fatalf("expected 999, got %d", cents)
	}
}

func TestWorker_TickAggregatorError_FailOpen(t *testing.T) {
	windowStart := time.Now().Add(-1 * time.Hour)
	lister := &fakeListLimited{accounts: []AccountRecord{
		{ID: 2, TenantID: 10, SessionWindow5hStart: windowStart},
	}}
	agg := &fakeAggregator{err: errors.New("query failed")}
	cache := NewCache()
	cache.Set(2, 800) // pre-existing value must be preserved

	w := NewWorker(lister, agg, cache, 0, nil)
	w.tick(context.Background())

	cents, fresh := cache.CurrentCost(2)
	if !fresh {
		t.Fatal("expected cache untouched on aggregator error")
	}
	if cents != 800 {
		t.Fatalf("expected 800, got %d", cents)
	}
}

func TestWorker_TickZeroWindowStart_Skipped(t *testing.T) {
	lister := &fakeListLimited{accounts: []AccountRecord{
		{ID: 3, TenantID: 10, SessionWindow5hStart: time.Time{}}, // zero
	}}
	agg := &fakeAggregator{cents: map[int64]int64{3: 1000}}
	cache := NewCache()

	w := NewWorker(lister, agg, cache, 0, nil)
	w.tick(context.Background())

	_, fresh := cache.CurrentCost(3)
	if fresh {
		t.Fatal("expected account with zero window start to be skipped (no cache entry)")
	}
}
