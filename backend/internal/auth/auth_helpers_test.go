// In-memory test stubs for F-AUTH-005 contract tests.
package auth

import (
	"context"
	"sync"
	"time"
)

// memCache is an in-memory TokenCache.
type memCache struct {
	mu     sync.Mutex
	values map[string]string
	ttls   map[string]time.Time
}

func newMemCache() *memCache {
	return &memCache{values: make(map[string]string), ttls: make(map[string]time.Time)}
}

func (c *memCache) Get(_ context.Context, key string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if exp, ok := c.ttls[key]; ok && time.Now().After(exp) {
		delete(c.values, key)
		delete(c.ttls, key)
		return "", nil
	}
	return c.values[key], nil
}

func (c *memCache) Set(_ context.Context, key, token string, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values[key] = token
	c.ttls[key] = time.Now().Add(ttl)
	return nil
}

// memLock is an in-memory RefreshLock.
type memLock struct {
	mu    sync.Mutex
	held  map[string]time.Time
}

func newMemLock() *memLock {
	return &memLock{held: make(map[string]time.Time)}
}

func (l *memLock) Acquire(_ context.Context, key string, ttl time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if exp, ok := l.held[key]; ok && time.Now().Before(exp) {
		return false, nil
	}
	l.held[key] = time.Now().Add(ttl)
	return true, nil
}

func (l *memLock) Release(_ context.Context, key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.held, key)
	return nil
}

// memStore is an in-memory AccountCredentialStore.
type memStore struct {
	mu       sync.Mutex
	accounts map[string]*ProviderAccountCredential
}

func newMemStore() *memStore {
	return &memStore{accounts: make(map[string]*ProviderAccountCredential)}
}

func (s *memStore) put(c ProviderAccountCredential) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := c
	s.accounts[storeKey(c.TenantID, c.AccountID)] = &cp
}

func (s *memStore) get(tenantID, accountID int64) *ProviderAccountCredential {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.accounts[storeKey(tenantID, accountID)]; ok {
		cp := *c
		return &cp
	}
	return nil
}

func (s *memStore) LoadProviderAccount(_ context.Context, tenantID, accountID int64) (ProviderAccountCredential, error) {
	if c := s.get(tenantID, accountID); c != nil {
		return *c, nil
	}
	return ProviderAccountCredential{}, ErrAccountUnavailable
}

func (s *memStore) SaveRefreshedCredential(_ context.Context, u RefreshedCredentialUpdate) (CredentialSaveResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := storeKey(u.TenantID, u.AccountID)
	cur, ok := s.accounts[key]
	if !ok {
		return CredentialSaveResult{}, ErrAccountUnavailable
	}
	if cur.TokenVersion != u.TokenVersion {
		cp := *cur
		return CredentialSaveResult{RowsAffected: 0, Winning: &cp}, nil
	}
	cur.CredentialJSON = u.CredentialJSON
	cur.TokenVersion = u.TokenVersion + 1
	cur.RefreshTokenFingerprint = u.RefreshTokenFingerprint
	return CredentialSaveResult{RowsAffected: 1}, nil
}

func storeKey(tenantID, accountID int64) string {
	return string(rune(tenantID)) + ":" + string(rune(accountID))
}

// memMarker is an in-memory AccountStateMarker capturing temp-unsched + operator-attention calls.
type memMarker struct {
	mu                 sync.Mutex
	tempUnsched        map[string]time.Time
	tempUnschedReasons map[string]string
	operatorAttention  map[string]string
}

func newMemMarker() *memMarker {
	return &memMarker{
		tempUnsched:        make(map[string]time.Time),
		tempUnschedReasons: make(map[string]string),
		operatorAttention:  make(map[string]string),
	}
}

func (m *memMarker) MarkTempUnschedulable(_ context.Context, tenantID, accountID int64, until time.Time, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := storeKey(tenantID, accountID)
	m.tempUnsched[k] = until
	m.tempUnschedReasons[k] = reason
	return nil
}

func (m *memMarker) MarkOperatorAttention(_ context.Context, tenantID, accountID int64, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.operatorAttention[storeKey(tenantID, accountID)] = reason
	return nil
}

// memAudit captures audit entries for assertions.
type memAudit struct {
	mu      sync.Mutex
	entries []RefreshAuditEntry
}

func newMemAudit() *memAudit { return &memAudit{} }

func (m *memAudit) WriteRefreshAudit(_ context.Context, e *RefreshAuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, *e)
	return nil
}

func (m *memAudit) byOutcome(o Outcome) []RefreshAuditEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []RefreshAuditEntry
	for _, e := range m.entries {
		if e.Outcome == o {
			out = append(out, e)
		}
	}
	return out
}

// goodToken is a 32-char alphanumeric value that passes attestTokenShape.
const goodToken = "abcdef1234567890ABCDEF1234567890XX"
