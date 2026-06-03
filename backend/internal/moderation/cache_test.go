package moderation

import (
	"context"
	"testing"
	"time"
)

func TestKeywordStore_CachesRowsWithinTTL(t *testing.T) {
	source := &keywordSourceStub{rules: []KeywordRule{{ID: 1, Keyword: "one"}}}
	store := NewKeywordStore(source, CacheOptions{MaxEntries: 8, TTL: time.Minute})

	first, err := store.ListEnabled(context.Background(), 7)
	if err != nil {
		t.Fatalf("first ListEnabled error: %v", err)
	}
	source.rules = []KeywordRule{{ID: 2, Keyword: "two"}}
	second, err := store.ListEnabled(context.Background(), 7)
	if err != nil {
		t.Fatalf("second ListEnabled error: %v", err)
	}

	if source.calls != 1 {
		t.Fatalf("source calls=%d want 1", source.calls)
	}
	if len(first) != 1 || len(second) != 1 || second[0].ID != first[0].ID {
		t.Fatalf("cache rows mismatch: first=%+v second=%+v", first, second)
	}
}

func TestKeywordStore_ExpiresRowsAfterTTL(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 6, 3, 1, 0, 0, 0, time.UTC)}
	source := &keywordSourceStub{rules: []KeywordRule{{ID: 1, Keyword: "one"}}}
	store := NewKeywordStore(source, CacheOptions{
		MaxEntries: 8,
		TTL:        time.Second,
		Now:        clock.Now,
	})

	if _, err := store.ListEnabled(context.Background(), 7); err != nil {
		t.Fatalf("first ListEnabled error: %v", err)
	}
	source.rules = []KeywordRule{{ID: 2, Keyword: "two"}}
	clock.now = clock.now.Add(2 * time.Second)
	second, err := store.ListEnabled(context.Background(), 7)
	if err != nil {
		t.Fatalf("second ListEnabled error: %v", err)
	}

	if source.calls != 2 {
		t.Fatalf("source calls=%d want 2 after TTL expiry", source.calls)
	}
	if len(second) != 1 || second[0].ID != 2 {
		t.Fatalf("expired cache did not refresh: %+v", second)
	}
}

type keywordSourceStub struct {
	rules []KeywordRule
	calls int
}

func (s *keywordSourceStub) ListEnabled(context.Context, int64) ([]KeywordRule, error) {
	s.calls++
	out := make([]KeywordRule, len(s.rules))
	copy(out, s.rules)
	return out, nil
}

type manualClock struct {
	now time.Time
}

func (c *manualClock) Now() time.Time {
	return c.now
}
