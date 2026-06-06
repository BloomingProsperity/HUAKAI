package announcement

import (
	"context"
	"sort"
	"sync"
	"time"
)

type MemoryStore struct {
	mu     sync.Mutex
	nextID int64
	rows   map[int64]Announcement
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		nextID: 1,
		rows:   make(map[int64]Announcement),
	}
}

func (s *MemoryStore) Create(_ context.Context, ann Announcement) (Announcement, error) {
	if s == nil {
		return Announcement{}, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ann.ID = s.nextID
	s.nextID++
	if ann.CreatedAt.IsZero() {
		ann.CreatedAt = time.Now().UTC()
	}
	if ann.UpdatedAt.IsZero() {
		ann.UpdatedAt = ann.CreatedAt
	}
	s.rows[ann.ID] = cloneAnnouncement(ann)
	return cloneAnnouncement(ann), nil
}

func (s *MemoryStore) Update(_ context.Context, ann Announcement) (Announcement, error) {
	if s == nil {
		return Announcement{}, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.rows[ann.ID]
	if !ok || current.TenantID != ann.TenantID {
		return Announcement{}, ErrNotFound
	}
	ann.CreatedAt = current.CreatedAt
	ann.CreatedByAdmin = int64Ptr(current.CreatedByAdmin)
	ann.UpdatedAt = time.Now().UTC()
	s.rows[ann.ID] = cloneAnnouncement(ann)
	return cloneAnnouncement(ann), nil
}

func (s *MemoryStore) Delete(_ context.Context, tenantID, id int64) error {
	if s == nil {
		return ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ann, ok := s.rows[id]
	if !ok || ann.TenantID != tenantID {
		return ErrNotFound
	}
	delete(s.rows, id)
	return nil
}

func (s *MemoryStore) Get(_ context.Context, tenantID, id int64) (Announcement, error) {
	if s == nil {
		return Announcement{}, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ann, ok := s.rows[id]
	if !ok || ann.TenantID != tenantID {
		return Announcement{}, ErrNotFound
	}
	return cloneAnnouncement(ann), nil
}

func (s *MemoryStore) ListActive(_ context.Context, in ListActiveInput) ([]Announcement, error) {
	if s == nil {
		return nil, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]Announcement, 0, len(s.rows))
	for _, ann := range s.rows {
		if ann.TenantID != in.TenantID || !ann.Active || ann.PublishedAt.After(in.Now) {
			continue
		}
		if ann.ExpiresAt != nil && !ann.ExpiresAt.After(in.Now) {
			continue
		}
		items = append(items, cloneAnnouncement(ann))
	}
	sortAnnouncements(items)
	return pageAnnouncements(items, in.Limit, in.Offset), nil
}

func (s *MemoryStore) ListAllAdmin(_ context.Context, in ListAdminInput) ([]Announcement, error) {
	if s == nil {
		return nil, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]Announcement, 0, len(s.rows))
	for _, ann := range s.rows {
		if ann.TenantID == in.TenantID {
			items = append(items, cloneAnnouncement(ann))
		}
	}
	sortAnnouncements(items)
	return pageAnnouncements(items, in.Limit, in.Offset), nil
}

func sortAnnouncements(items []Announcement) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].PublishedAt.Equal(items[j].PublishedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].PublishedAt.After(items[j].PublishedAt)
	})
}

func pageAnnouncements(items []Announcement, limit, offset int) []Announcement {
	if offset >= len(items) {
		return []Announcement{}
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	out := make([]Announcement, end-offset)
	copy(out, items[offset:end])
	return out
}

func cloneAnnouncement(ann Announcement) Announcement {
	ann.ExpiresAt = utcTimePtr(ann.ExpiresAt)
	ann.CreatedByAdmin = int64Ptr(ann.CreatedByAdmin)
	return ann
}
