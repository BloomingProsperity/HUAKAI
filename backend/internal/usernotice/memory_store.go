package usernotice

import (
	"context"
	"sort"
	"sync"
	"time"
)

type MemoryStore struct {
	mu          sync.Mutex
	nextID      int64
	rows        map[int64]Notification
	activeUsers map[int64]map[int64]struct{}
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		nextID:      1,
		rows:        make(map[int64]Notification),
		activeUsers: make(map[int64]map[int64]struct{}),
	}
}

func (s *MemoryStore) AddActiveUser(tenantID, userID int64) {
	if s == nil || tenantID <= 0 || userID <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeUsers[tenantID] == nil {
		s.activeUsers[tenantID] = make(map[int64]struct{})
	}
	s.activeUsers[tenantID][userID] = struct{}{}
}

func (s *MemoryStore) BroadcastInsert(_ context.Context, notice Notification, maxRecipients int) (broadcastStoreResult, error) {
	if s == nil {
		return broadcastStoreResult{}, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	userIDs := make([]int64, 0, len(s.activeUsers[notice.TenantID]))
	for userID := range s.activeUsers[notice.TenantID] {
		userIDs = append(userIDs, userID)
	}
	sort.Slice(userIDs, func(i, j int) bool { return userIDs[i] < userIDs[j] })
	if maxRecipients > 0 && len(userIDs) > maxRecipients {
		return broadcastStoreResult{Capped: true}, nil
	}
	for _, userID := range userIDs {
		row := cloneNotification(notice)
		row.ID = s.nextID
		row.UserID = userID
		s.nextID++
		if row.CreatedAt.IsZero() {
			row.CreatedAt = time.Now().UTC()
		}
		s.rows[row.ID] = cloneNotification(row)
	}
	return broadcastStoreResult{Inserted: int64(len(userIDs))}, nil
}

func (s *MemoryStore) ListForUser(_ context.Context, in ListInput) ([]Notification, error) {
	if s == nil {
		return nil, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]Notification, 0, len(s.rows))
	for _, row := range s.rows {
		if row.TenantID != in.TenantID || row.UserID != in.UserID {
			continue
		}
		if in.UnreadOnly && row.ReadAt != nil {
			continue
		}
		items = append(items, cloneNotification(row))
	}
	sortNotifications(items)
	return pageNotifications(items, in.Limit, in.Offset), nil
}

func (s *MemoryStore) MarkRead(_ context.Context, tenantID, userID, id int64, readAt time.Time) (Notification, error) {
	if s == nil {
		return Notification{}, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok || row.TenantID != tenantID || row.UserID != userID {
		return Notification{}, ErrNotFound
	}
	if row.ReadAt == nil {
		t := readAt.UTC()
		row.ReadAt = &t
		s.rows[id] = cloneNotification(row)
	}
	return cloneNotification(row), nil
}

func (s *MemoryStore) UnreadCount(_ context.Context, tenantID, userID int64) (int64, error) {
	if s == nil {
		return 0, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int64
	for _, row := range s.rows {
		if row.TenantID == tenantID && row.UserID == userID && row.ReadAt == nil {
			count++
		}
	}
	return count, nil
}

func sortNotifications(items []Notification) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
}

func pageNotifications(items []Notification, limit, offset int) []Notification {
	if offset >= len(items) {
		return []Notification{}
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	out := make([]Notification, end-offset)
	copy(out, items[offset:end])
	return out
}

func cloneNotification(in Notification) Notification {
	in.ReadAt = utcTimePtr(in.ReadAt)
	in.CreatedByAdmin = int64Ptr(in.CreatedByAdmin)
	return in
}
