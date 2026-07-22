// 用于 F-POOL-001 契约测试的内存 test stub。
package router

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// stubAccountSource 返回一份固定的账号列表。
type stubAccountSource struct {
	accounts []*AccountSnapshot
}

func (s *stubAccountSource) ListAccounts(_ context.Context, req SelectionRequest) ([]*AccountSnapshot, error) {
	// 镜像生产 SQL 查询(sql/queries/pool_accounts.sql ListEligibleAccounts):
	// WHERE tenant_id = $1 AND enabled = true AND deleted_at IS NULL ...
	out := make([]*AccountSnapshot, 0, len(s.accounts))
	for _, a := range s.accounts {
		if a.TenantID == req.TenantID {
			out = append(out, a)
		}
	}
	return out, nil
}

// stubPolicy 返回一个固定的 RoutingPolicy。
type stubPolicy struct{ p *RoutingPolicy }

func (s *stubPolicy) GetRoutingPolicy(_ context.Context, _ SelectionRequest) (*RoutingPolicy, error) {
	return s.p, nil
}

// stubSticky 把 session_hash 解析为 accountID。
type stubSticky struct {
	bindings map[string]int64
}

func (s *stubSticky) Lookup(_ context.Context, req SelectionRequest) (int64, bool, error) {
	if id, ok := s.bindings[req.SessionHash]; ok {
		return id, true, nil
	}
	return 0, false, nil
}

// captureClaimGate 记录 WriteAcquisition 的调用。
type captureClaimGate struct {
	mu    sync.Mutex
	calls []claimWrite
}

type claimWrite struct {
	TenantID  int64
	ClaimID   int64
	AccountID int64
	Token     uuid.UUID
}

func (c *captureClaimGate) WriteAcquisition(_ context.Context, tenantID, claimID, accountID int64, token uuid.UUID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, claimWrite{tenantID, claimID, accountID, token})
	return nil
}

// memSlotManager 发放 token; 并跟踪释放。
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

// snap 是构造 AccountSnapshot 字面量的简写。
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
