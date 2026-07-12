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

func (s *MemoryStore) MarkTOTPSuccess(_ context.Context, tenantID, userID int64, consumedStep int64, now time.Time) (bool, error) {
	if s == nil {
		return false, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := userKey(tenantID, userID)
	settings, ok := s.settings[key]
	if !ok {
		return false, ErrNotSetup
	}
	// 条件守卫:仅当 consumedStep 严格大于已存 LastUsedStep(或其为 nil)时才记录,镜像
	// Postgres 条件 UPDATE 的原子语义——并发提交同一时间步只有一个成功(stored=true),
	// 其余返回 stored=false 供调用方按重放拒绝。锁持有期间完成"比较+写入"保证原子。
	if settings.LastUsedStep != nil && consumedStep <= *settings.LastUsedStep {
		return false, nil
	}
	settings.FailedAttempts = 0
	settings.LockedUntil = nil
	settings.LastUsedAt = cloneTimePtr(&now)
	step := consumedStep
	settings.LastUsedStep = &step
	settings.UpdatedAt = now
	s.settings[key] = settings
	return true, nil
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
	in.LastUsedStep = cloneInt64Ptr(in.LastUsedStep)
	return in
}
