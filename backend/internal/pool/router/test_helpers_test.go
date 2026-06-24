// In-memory test stubs for F-POOL-001 contract tests.
package router

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// stubAccountSource returns a fixed account list.
type stubAccountSource struct {
	accounts []*AccountSnapshot
}

func (s *stubAccountSource) ListAccounts(_ context.Context, req SelectionRequest) ([]*AccountSnapshot, error) {
	// Mirror production SQL query (sql/queries/pool_accounts.sql ListEligibleAccounts):
	// WHERE tenant_id = $1 AND enabled = true AND deleted_at IS NULL ...
	out := make([]*AccountSnapshot, 0, len(s.accounts))
	for _, a := range s.accounts {
		if a.TenantID == req.TenantID {
			out = append(out, a)
		}
	}
	return out, nil
}

// stubPolicy returns a fixed RoutingPolicy.
type stubPolicy struct{ p *RoutingPolicy }

func (s *stubPolicy) GetRoutingPolicy(_ context.Context, _ SelectionRequest) (*RoutingPolicy, error) {
	return s.p, nil
}

// stubSticky resolves session_hash → accountID.
type stubSticky struct {
	bindings map[string]int64
}

func (s *stubSticky) Lookup(_ context.Context, req SelectionRequest) (int64, bool, error) {
	if id, ok := s.bindings[req.SessionHash]; ok {
		return id, true, nil
	}
	return 0, false, nil
}

// memSlotManager hands out tokens; tracks releases.
type memSlotManager struct {
	mu         sync.Mutex
	releases   map[uuid.UUID]int
	releaseFns map[uuid.UUID]ReleaseFunc
}

func newMemSlotManager() *memSlotManager {
	return &memSlotManager{
		releases:   make(map[uuid.UUID]int),
		releaseFns: make(map[uuid.UUID]ReleaseFunc),
	}
}

func (m *memSlotManager) Acquire(_ context.Context, account *AccountSnapshot, _ SelectionRequest) (*AcquireResult, error) {
	tok := uuid.New()
	release := NewIdempotentRelease(tok, func(_ context.Context) error {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.releases[tok]++
		return nil
	})
	m.mu.Lock()
	m.releaseFns[tok] = release
	m.mu.Unlock()
	return &AcquireResult{AcquisitionToken: tok, Release: release}, nil
}

func (m *memSlotManager) releaseCount(tok uuid.UUID) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.releases[tok]
}

// snap is shorthand for an AccountSnapshot literal.
func snap(id, tenant int64, prio int, load float64, lastUsed time.Time) *AccountSnapshot {
	return &AccountSnapshot{
		ID:             id,
		TenantID:       tenant,
		Priority:       prio,
		LoadRate:       load,
		LastUsedAt:     lastUsed,
		MaxConcurrency: 4,
	}
}
