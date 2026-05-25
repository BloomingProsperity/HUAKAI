package cache

import (
	"context"
	"testing"
	"time"
)

func TestMemoryStoreLRUEvictsBySize(t *testing.T) {
	now := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	store := NewMemoryStoreWithClock(10, time.Minute, func() time.Time { return now })
	store.Set(context.Background(), Entry{Key: "a", TenantID: 1, Vendor: "openai", Model: "m", Body: []byte("aaaa")})
	store.Set(context.Background(), Entry{Key: "b", TenantID: 1, Vendor: "openai", Model: "m", Body: []byte("bbbb")})
	if _, ok := store.Get(context.Background(), "a"); !ok {
		t.Fatal("a should exist before capacity pressure")
	}
	store.Set(context.Background(), Entry{Key: "c", TenantID: 1, Vendor: "openai", Model: "m", Body: []byte("cccc")})
	if _, ok := store.Get(context.Background(), "b"); ok {
		t.Fatal("b should be evicted as least recently used")
	}
	if _, ok := store.Get(context.Background(), "a"); !ok {
		t.Fatal("a should survive because it was recently read")
	}
	if _, ok := store.Get(context.Background(), "c"); !ok {
		t.Fatal("c should be stored")
	}
}

func TestMemoryStoreTTLExpiry(t *testing.T) {
	now := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	store := NewMemoryStoreWithClock(100, time.Second, func() time.Time { return now })
	store.Set(context.Background(), Entry{Key: "a", TenantID: 1, Vendor: "openai", Model: "m", Body: []byte("body")})
	now = now.Add(2 * time.Second)
	if _, ok := store.Get(context.Background(), "a"); ok {
		t.Fatal("expired entry should miss")
	}
	if stats := store.Stats(context.Background()); stats.SizeBytes != 0 || len(stats.Entries) != 0 {
		t.Fatalf("expired entry should be removed from stats: %+v", stats)
	}
}
