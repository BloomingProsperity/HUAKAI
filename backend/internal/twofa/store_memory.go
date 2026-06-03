package twofa

import (
	"context"
	"strconv"
	"sync"
	"time"
)

type MemoryStore struct {
	mu       sync.Mutex
	settings map[string]Settings
	codes    map[string]map[string]backupCodeState
}

type backupCodeState struct {
	Hash      []byte
	Used      bool
	UsedAt    *time.Time
	CreatedAt time.Time
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		settings: map[string]Settings{},
		codes:    map[string]map[string]backupCodeState{},
	}
}

func (s *MemoryStore) GetSettings(_ context.Context, tenantID, userID int64) (Settings, bool, error) {
	if s == nil {
		return Settings{}, false, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, ok := s.settings[userKey(tenantID, userID)]
	if !ok {
		return Settings{}, false, nil
	}
	return cloneSettings(settings), true, nil
}

func (s *MemoryStore) SaveSetup(_ context.Context, settings Settings, backupCodeHashes [][]byte) error {
	if s == nil {
		return ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := userKey(settings.TenantID, settings.UserID)
	s.settings[key] = cloneSettings(settings)
	s.codes[key] = statesFromHashes(backupCodeHashes, settings.CreatedAt)
	return nil
}

func (s *MemoryStore) SetEnabled(_ context.Context, tenantID, userID int64, enabled bool, now time.Time) error {
	if s == nil {
		return ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := userKey(tenantID, userID)
	settings, ok := s.settings[key]
	if !ok {
		return ErrNotSetup
	}
	settings.Enabled = enabled
	settings.FailedAttempts = 0
	settings.LockedUntil = nil
	if enabled {
		settings.LastUsedAt = cloneTimePtr(&now)
	}
	settings.UpdatedAt = now
	s.settings[key] = settings
	return nil
}

func (s *MemoryStore) MarkSuccess(_ context.Context, tenantID, userID int64, now time.Time) error {
	if s == nil {
		return ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := userKey(tenantID, userID)
	settings, ok := s.settings[key]
	if !ok {
		return ErrNotSetup
	}
	settings.FailedAttempts = 0
	settings.LockedUntil = nil
	settings.LastUsedAt = cloneTimePtr(&now)
	settings.UpdatedAt = now
	s.settings[key] = settings
	return nil
}

func (s *MemoryStore) MarkFailure(_ context.Context, tenantID, userID int64, failedAttempts int, lockedUntil *time.Time, now time.Time) error {
	if s == nil {
		return ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := userKey(tenantID, userID)
	settings, ok := s.settings[key]
	if !ok {
		return ErrNotSetup
	}
	settings.FailedAttempts = failedAttempts
	settings.LockedUntil = cloneTimePtr(lockedUntil)
	settings.UpdatedAt = now
	s.settings[key] = settings
	return nil
}

func (s *MemoryStore) CountUnusedBackupCodes(_ context.Context, tenantID, userID int64) (int, error) {
	if s == nil {
		return 0, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, state := range s.codes[userKey(tenantID, userID)] {
		if !state.Used {
			count++
		}
	}
	return count, nil
}

func (s *MemoryStore) ConsumeBackupCode(_ context.Context, tenantID, userID int64, hash []byte, now time.Time) (bool, error) {
	if s == nil {
		return false, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := userKey(tenantID, userID)
	codeKey := string(hash)
	state, ok := s.codes[key][codeKey]
	if !ok || state.Used {
		return false, nil
	}
	state.Used = true
	state.UsedAt = cloneTimePtr(&now)
	s.codes[key][codeKey] = state
	return true, nil
}

func (s *MemoryStore) ReplaceBackupCodes(_ context.Context, tenantID, userID int64, hashes [][]byte, now time.Time) error {
	if s == nil {
		return ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := userKey(tenantID, userID)
	if _, ok := s.settings[key]; !ok {
		return ErrNotSetup
	}
	s.codes[key] = statesFromHashes(hashes, now)
	return nil
}

func statesFromHashes(hashes [][]byte, createdAt time.Time) map[string]backupCodeState {
	out := make(map[string]backupCodeState, len(hashes))
	for _, hash := range hashes {
		copied := append([]byte(nil), hash...)
		out[string(copied)] = backupCodeState{Hash: copied, CreatedAt: createdAt}
	}
	return out
}

func userKey(tenantID, userID int64) string {
	return strconv.FormatInt(tenantID, 10) + ":" + strconv.FormatInt(userID, 10)
}

func cloneSettings(in Settings) Settings {
	in.SecretEnc = append([]byte(nil), in.SecretEnc...)
	in.LockedUntil = cloneTimePtr(in.LockedUntil)
	in.LastUsedAt = cloneTimePtr(in.LastUsedAt)
	return in
}
