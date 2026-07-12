package passkey

import (
	"context"
	"encoding/base64"
	"sort"
	"strconv"
	"sync"
	"time"
)

type Store interface {
	SaveCredential(context.Context, CredentialRecord) (CredentialRecord, error)
	ListCredentials(context.Context, int64, int64) ([]CredentialRecord, error)
	GetCredentialByID(context.Context, int64, int64) (CredentialRecord, error)
	GetCredentialByCredentialID(context.Context, int64, []byte) (CredentialRecord, error)
	DeleteCredential(context.Context, int64, int64, int64) error
	UpdateCredentialUsage(context.Context, int64, []byte, uint32, bool, time.Time) (CredentialRecord, error)
	FlagCredentialCloneWarning(context.Context, int64, []byte, time.Time) error
	SaveCeremonySession(context.Context, CeremonySession) error
	ConsumeCeremonySession(context.Context, ConsumeCeremonyInput) (CeremonySession, error)
}

type ConsumeCeremonyInput struct {
	ID       string
	TenantID int64
	UserID   int64
	Purpose  string
	Now      time.Time
}

type MemoryStore struct {
	mu              sync.Mutex
	nextID          int64
	credentials     map[int64]CredentialRecord
	credentialByKey map[string]int64
	ceremonies      map[string]CeremonySession
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		nextID:          1,
		credentials:     map[int64]CredentialRecord{},
		credentialByKey: map[string]int64{},
		ceremonies:      map[string]CeremonySession{},
	}
}

func (s *MemoryStore) SaveCredential(_ context.Context, record CredentialRecord) (CredentialRecord, error) {
	if s == nil {
		return CredentialRecord{}, ErrStoreNotConfigured
	}
	if record.TenantID <= 0 || record.UserID <= 0 || len(record.CredentialID) == 0 || len(record.PublicKey) == 0 {
		return CredentialRecord{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := credentialKey(record.TenantID, record.CredentialID)
	if _, exists := s.credentialByKey[key]; exists {
		return CredentialRecord{}, ErrDuplicateCredential
	}
	now := record.CreatedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	record.ID = s.nextID
	s.nextID++
	record.CreatedAt = now
	record.Name = cleanName(record.Name)
	record.CredentialID = append([]byte(nil), record.CredentialID...)
	record.PublicKey = append([]byte(nil), record.PublicKey...)
	record.AAGUID = append([]byte(nil), record.AAGUID...)
	record.Transports = append([]string(nil), record.Transports...)
	record.LastUsedAt = cloneTime(record.LastUsedAt)
	s.credentials[record.ID] = record
	s.credentialByKey[key] = record.ID
	return cloneCredential(record), nil
}

func (s *MemoryStore) ListCredentials(_ context.Context, tenantID, userID int64) ([]CredentialRecord, error) {
	if s == nil {
		return nil, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CredentialRecord, 0)
	for _, record := range s.credentials {
		if record.TenantID == tenantID && record.UserID == userID {
			out = append(out, cloneCredential(record))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (s *MemoryStore) GetCredentialByID(_ context.Context, tenantID, id int64) (CredentialRecord, error) {
	if s == nil {
		return CredentialRecord{}, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.credentials[id]
	if !ok || record.TenantID != tenantID {
		return CredentialRecord{}, ErrCredentialNotFound
	}
	return cloneCredential(record), nil
}

func (s *MemoryStore) GetCredentialByCredentialID(_ context.Context, tenantID int64, credentialID []byte) (CredentialRecord, error) {
	if s == nil {
		return CredentialRecord{}, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.credentialByKey[credentialKey(tenantID, credentialID)]
	if !ok {
		return CredentialRecord{}, ErrCredentialNotFound
	}
	return cloneCredential(s.credentials[id]), nil
}

func (s *MemoryStore) DeleteCredential(_ context.Context, tenantID, userID, id int64) error {
	if s == nil {
		return ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.credentials[id]
	if !ok || record.TenantID != tenantID || record.UserID != userID {
		return ErrCredentialNotFound
	}
	delete(s.credentials, id)
	delete(s.credentialByKey, credentialKey(record.TenantID, record.CredentialID))
	return nil
}

func (s *MemoryStore) UpdateCredentialUsage(_ context.Context, tenantID int64, credentialID []byte, signCount uint32, cloneWarning bool, now time.Time) (CredentialRecord, error) {
	if s == nil {
		return CredentialRecord{}, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.credentialByKey[credentialKey(tenantID, credentialID)]
	if !ok {
		return CredentialRecord{}, ErrCredentialNotFound
	}
	record := s.credentials[id]
	// 与 PostgresStore 的 CAS 守卫对齐:仅允许严格递增(或双 0)写入,挡并发竞态
	// 绕过克隆检测;否则返 ErrCloneDetected,令测试形态与生产形态行为一致。
	if !(record.SignCount < signCount || (record.SignCount == 0 && signCount == 0)) {
		return CredentialRecord{}, ErrCloneDetected
	}
	t := now.UTC()
	record.SignCount = signCount
	// clone_warning 只升不降:正常递增登录不得抹掉历史置位的告警(防克隆信号是
	// "曾疑似克隆"的持久标志,真克隆者也会有正常计数那一半)。
	record.CloneWarning = record.CloneWarning || cloneWarning
	record.LastUsedAt = &t
	s.credentials[id] = record
	return cloneCredential(record), nil
}

func (s *MemoryStore) FlagCredentialCloneWarning(_ context.Context, tenantID int64, credentialID []byte, _ time.Time) error {
	if s == nil {
		return ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.credentialByKey[credentialKey(tenantID, credentialID)]
	if !ok {
		return ErrCredentialNotFound
	}
	record := s.credentials[id]
	record.CloneWarning = true
	s.credentials[id] = record
	return nil
}

func (s *MemoryStore) SaveCeremonySession(_ context.Context, session CeremonySession) error {
	if s == nil {
		return ErrStoreNotConfigured
	}
	if session.ID == "" || session.TenantID <= 0 || session.Purpose == "" || len(session.SessionData) == 0 || session.ExpiresAt.IsZero() {
		return ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session.SessionData = append([]byte(nil), session.SessionData...)
	session.ExpiresAt = session.ExpiresAt.UTC()
	session.CreatedAt = session.CreatedAt.UTC()
	s.ceremonies[session.ID] = session
	return nil
}

func (s *MemoryStore) ConsumeCeremonySession(_ context.Context, in ConsumeCeremonyInput) (CeremonySession, error) {
	if s == nil {
		return CeremonySession{}, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.ceremonies[in.ID]
	if !ok || session.TenantID != in.TenantID || session.Purpose != in.Purpose {
		return CeremonySession{}, ErrCeremonyNotFound
	}
	if in.UserID > 0 && session.UserID != in.UserID {
		return CeremonySession{}, ErrCeremonyNotFound
	}
	if in.UserID == 0 && session.UserID != 0 {
		return CeremonySession{}, ErrCeremonyNotFound
	}
	delete(s.ceremonies, in.ID)
	if !session.ExpiresAt.After(in.Now.UTC()) {
		return CeremonySession{}, ErrCeremonyExpired
	}
	session.SessionData = append([]byte(nil), session.SessionData...)
	return session, nil
}

func cloneCredential(record CredentialRecord) CredentialRecord {
	record.CredentialID = append([]byte(nil), record.CredentialID...)
	record.PublicKey = append([]byte(nil), record.PublicKey...)
	record.AAGUID = append([]byte(nil), record.AAGUID...)
	record.Transports = append([]string(nil), record.Transports...)
	record.LastUsedAt = cloneTime(record.LastUsedAt)
	return record
}

func credentialKey(tenantID int64, credentialID []byte) string {
	return strconv.FormatInt(tenantID, 10) + ":" + base64.RawURLEncoding.EncodeToString(credentialID)
}
