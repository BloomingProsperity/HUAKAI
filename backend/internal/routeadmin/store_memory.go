// HUAKAI · iKun

package routeadmin

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemoryStore 是测试用内存实现, 模拟 (tenant_id, name) 唯一(未软删)、软删、可选的 pool_group FK。
type MemoryStore struct {
	mu      sync.Mutex
	seq     int64
	routes  map[int64]Route
	deleted map[int64]bool
	// poolGroups 非 nil 时模拟 FK + 租户归属: id→owning tenant。
	// 创建时 pool_group 不存在 或 owning tenant ≠ 入参 tenant → ErrPoolGroupNotFound。
	poolGroups map[int64]int64
	now        time.Time
}

// NewMemoryStore 构造内存 store(固定时钟, 不依赖 time.Now 当随机源)。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		routes:  map[int64]Route{},
		deleted: map[int64]bool{},
		now:     time.Unix(1700000000, 0).UTC(),
	}
}

// WithPoolGroup 注册一个属于 tenantID 的 pool_group(模拟外键 + 租户归属); 可链式多次调用。
// 一旦调用过(poolGroups 非 nil)即开启校验: 未注册或归属不符的 pool_group 创建即 ErrPoolGroupNotFound。
func (m *MemoryStore) WithPoolGroup(id, tenantID int64) *MemoryStore {
	if m.poolGroups == nil {
		m.poolGroups = map[int64]int64{}
	}
	m.poolGroups[id] = tenantID
	return m
}

func (m *MemoryStore) Create(_ context.Context, in CreateInput) (Route, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.poolGroups != nil {
		if owner, ok := m.poolGroups[in.PoolGroupID]; !ok || owner != in.TenantID {
			return Route{}, ErrPoolGroupNotFound
		}
	}
	for id, r := range m.routes {
		if m.deleted[id] {
			continue
		}
		if r.TenantID == in.TenantID && r.Name == in.Name {
			return Route{}, ErrDuplicateName
		}
	}
	m.seq++
	mp := 100
	if in.MatchPriority != nil {
		mp = *in.MatchPriority
	}
	r := Route{
		ID: m.seq, TenantID: in.TenantID, Name: in.Name,
		UserGroupMatch: in.UserGroupMatch, ModelPatternMatch: in.ModelPatternMatch,
		PoolGroupID: in.PoolGroupID, MatchPriority: mp, Enabled: true,
		CreatedAt: m.now, UpdatedAt: m.now,
	}
	m.routes[r.ID] = r
	return r, nil
}

func (m *MemoryStore) List(_ context.Context, tenantID int64) ([]Route, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Route
	for id, r := range m.routes {
		if m.deleted[id] || r.TenantID != tenantID {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].MatchPriority != out[j].MatchPriority {
			return out[i].MatchPriority < out[j].MatchPriority
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *MemoryStore) Get(_ context.Context, tenantID, id int64) (Route, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.routes[id]
	if !ok || m.deleted[id] || r.TenantID != tenantID {
		return Route{}, ErrRouteNotFound
	}
	return r, nil
}

func (m *MemoryStore) SoftDelete(_ context.Context, tenantID, id int64) (Route, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.routes[id]
	if !ok || m.deleted[id] || r.TenantID != tenantID {
		return Route{}, ErrRouteNotFound
	}
	m.deleted[id] = true
	return r, nil
}
