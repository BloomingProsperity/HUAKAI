package cache

import (
	"container/list"
	"context"
	"sync"
	"time"
)

// Entry 是一条已 canonical 化后的 non-streaming response cache 记录。
type Entry struct {
	Key       string
	TenantID  int64
	ScopeID   int64
	Vendor    string
	Model     string
	Status    int
	Body      []byte
	Envelope  []byte
	SizeBytes int64
	StoredAt  time.Time
	ExpiresAt time.Time
}

// EntryStats 是 admin stats 暴露的安全元数据，不包含 response body。
type EntryStats struct {
	Key       string    `json:"key"`
	TenantID  int64     `json:"tenant_id"`
	Vendor    string    `json:"vendor"`
	Model     string    `json:"model"`
	Status    int       `json:"status"`
	SizeBytes int64     `json:"size_bytes"`
	StoredAt  time.Time `json:"stored_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type VendorModelSize struct {
	Vendor    string
	Model     string
	SizeBytes int64
}

// Stats 是 MemoryStore 当前状态的快照。
type Stats struct {
	Enabled      bool
	Entries      []EntryStats
	SizeBytes    int64
	MaxSizeBytes int64
	TTLSeconds   int64
	ByLabel      []VendorModelSize
}

// Store 是 handler 和 admin surface 依赖的 L2 response cache 接口。
type Store interface {
	Get(ctx context.Context, key string) (Entry, bool)
	Set(ctx context.Context, entry Entry) bool
	Delete(ctx context.Context, key string) bool
	Stats(ctx context.Context) Stats
}

type memoryItem struct {
	entry Entry
}

// MemoryStore 是进程内 LRU + TTL store。容量按 body+envelope 字节限制。
type MemoryStore struct {
	mu         sync.Mutex
	ll         *list.List
	items      map[string]*list.Element
	totalBytes int64
	maxBytes   int64
	ttl        time.Duration
	now        func() time.Time
}

func NewMemoryStore(maxBytes int64, ttl time.Duration) *MemoryStore {
	return NewMemoryStoreWithClock(maxBytes, ttl, time.Now)
}

func NewMemoryStoreWithClock(maxBytes int64, ttl time.Duration, now func() time.Time) *MemoryStore {
	if now == nil {
		now = time.Now
	}
	return &MemoryStore{
		ll:         list.New(),
		items:      make(map[string]*list.Element),
		maxBytes:   maxBytes,
		ttl:        ttl,
		now:        now,
		totalBytes: 0,
	}
}

func (s *MemoryStore) Get(_ context.Context, key string) (Entry, bool) {
	if s == nil || key == "" {
		return Entry{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	el := s.items[key]
	if el == nil {
		return Entry{}, false
	}
	item := el.Value.(*memoryItem)
	if s.expired(item.entry, s.now()) {
		s.removeElement(el)
		return Entry{}, false
	}
	s.ll.MoveToFront(el)
	return cloneEntry(item.entry), true
}

func (s *MemoryStore) Set(_ context.Context, entry Entry) bool {
	if s == nil || entry.Key == "" || s.maxBytes <= 0 || s.ttl <= 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	entry.Body = cloneBytes(entry.Body)
	entry.Envelope = cloneBytes(entry.Envelope)
	entry.SizeBytes = entrySize(entry)
	entry.StoredAt = now
	entry.ExpiresAt = now.Add(s.ttl)
	if entry.SizeBytes <= 0 || entry.SizeBytes > s.maxBytes {
		if el := s.items[entry.Key]; el != nil {
			s.removeElement(el)
		}
		return false
	}
	if el := s.items[entry.Key]; el != nil {
		s.removeElement(el)
	}
	el := s.ll.PushFront(&memoryItem{entry: entry})
	s.items[entry.Key] = el
	s.totalBytes += entry.SizeBytes
	s.evictExpiredLocked(now)
	s.evictOverCapacityLocked()
	return true
}

func (s *MemoryStore) Delete(_ context.Context, key string) bool {
	if s == nil || key == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	el := s.items[key]
	if el == nil {
		return false
	}
	s.removeElement(el)
	return true
}

func (s *MemoryStore) Stats(_ context.Context) Stats {
	if s == nil {
		return Stats{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.evictExpiredLocked(now)

	stats := Stats{
		Enabled:      true,
		Entries:      make([]EntryStats, 0, len(s.items)),
		SizeBytes:    s.totalBytes,
		MaxSizeBytes: s.maxBytes,
		TTLSeconds:   int64(s.ttl.Seconds()),
	}
	byLabel := make(map[string]VendorModelSize)
	for el := s.ll.Front(); el != nil; el = el.Next() {
		item := el.Value.(*memoryItem)
		e := item.entry
		stats.Entries = append(stats.Entries, EntryStats{
			Key:       e.Key,
			TenantID:  e.TenantID,
			Vendor:    e.Vendor,
			Model:     e.Model,
			Status:    e.Status,
			SizeBytes: e.SizeBytes,
			StoredAt:  e.StoredAt,
			ExpiresAt: e.ExpiresAt,
		})
		labelKey := e.Vendor + "\x00" + e.Model
		row := byLabel[labelKey]
		row.Vendor = e.Vendor
		row.Model = e.Model
		row.SizeBytes += e.SizeBytes
		byLabel[labelKey] = row
	}
	for _, row := range byLabel {
		stats.ByLabel = append(stats.ByLabel, row)
	}
	return stats
}

func (s *MemoryStore) expired(entry Entry, now time.Time) bool {
	return !entry.ExpiresAt.IsZero() && !entry.ExpiresAt.After(now)
}

func (s *MemoryStore) evictExpiredLocked(now time.Time) {
	for el := s.ll.Back(); el != nil; {
		prev := el.Prev()
		if s.expired(el.Value.(*memoryItem).entry, now) {
			s.removeElement(el)
		}
		el = prev
	}
}

func (s *MemoryStore) evictOverCapacityLocked() {
	for s.totalBytes > s.maxBytes {
		el := s.ll.Back()
		if el == nil {
			return
		}
		s.removeElement(el)
	}
}

func (s *MemoryStore) removeElement(el *list.Element) {
	if el == nil {
		return
	}
	s.ll.Remove(el)
	item := el.Value.(*memoryItem)
	delete(s.items, item.entry.Key)
	s.totalBytes -= item.entry.SizeBytes
	if s.totalBytes < 0 {
		s.totalBytes = 0
	}
}

func entrySize(entry Entry) int64 {
	if entry.SizeBytes > 0 {
		return entry.SizeBytes
	}
	return int64(len(entry.Body) + len(entry.Envelope))
}

func cloneEntry(entry Entry) Entry {
	entry.Body = cloneBytes(entry.Body)
	entry.Envelope = cloneBytes(entry.Envelope)
	return entry
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}
