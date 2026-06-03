package platformsettings

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type MemoryStore struct {
	mu     sync.Mutex
	nextID int64
	rows   map[string]StoredSetting
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rows: make(map[string]StoredSetting)}
}

func (s *MemoryStore) Get(_ context.Context, scope, key string) (StoredSetting, bool, error) {
	if s == nil {
		return StoredSetting{}, false, ErrStoreNotConfigured
	}
	scope = strings.TrimSpace(scope)
	key = strings.TrimSpace(key)
	if scope == "" || key == "" {
		return StoredSetting{}, false, fmt.Errorf("%w: scope/key", ErrInvalidValue)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[memoryRowKey(scope, key)]
	return row, ok, nil
}

func (s *MemoryStore) List(_ context.Context, scope string) ([]StoredSetting, error) {
	if s == nil {
		return nil, ErrStoreNotConfigured
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return nil, fmt.Errorf("%w: scope", ErrInvalidValue)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]StoredSetting, 0, len(s.rows))
	for _, row := range s.rows {
		if row.Scope == scope {
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Key < out[j].Key
	})
	return out, nil
}

func (s *MemoryStore) Upsert(_ context.Context, scope, key, value, updatedBy string) (StoredSetting, error) {
	if s == nil {
		return StoredSetting{}, ErrStoreNotConfigured
	}
	scope = strings.TrimSpace(scope)
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	updatedBy = strings.TrimSpace(updatedBy)
	if scope == "" || key == "" || value == "" {
		return StoredSetting{}, fmt.Errorf("%w: scope/key/value", ErrInvalidValue)
	}
	if updatedBy == "" {
		updatedBy = "system"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	mapKey := memoryRowKey(scope, key)
	row := s.rows[mapKey]
	if row.ID == 0 {
		s.nextID++
		row.ID = s.nextID
	}
	row.Scope = scope
	row.Key = SettingKey(key)
	row.Value = value
	row.UpdatedAt = time.Now().UTC()
	row.UpdatedBy = updatedBy
	s.rows[mapKey] = row
	return row, nil
}

func memoryRowKey(scope, key string) string {
	return scope + "\x00" + key
}

var _ Store = (*MemoryStore)(nil)
