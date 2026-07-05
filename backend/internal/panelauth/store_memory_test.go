// HUAKAI · iKun

package panelauth

import "context"

// MemoryRoleStore 测试用内存 role 仓储, key=(tenantID,userID)。
type MemoryRoleStore struct {
	roles    map[[2]int64]string
	inactive map[[2]int64]bool // 存在但非 active(封禁/锁定): UserRole 查得到, ActiveUserRole 视其不存在
}

// NewMemoryRoleStore 构造空内存仓储。
func NewMemoryRoleStore() *MemoryRoleStore {
	return &MemoryRoleStore{roles: make(map[[2]int64]string), inactive: make(map[[2]int64]bool)}
}

// WithUser 链式登记一个 (tenant,user) → role(默认 active)。
func (s *MemoryRoleStore) WithUser(tenantID, userID int64, role string) *MemoryRoleStore {
	s.roles[[2]int64{tenantID, userID}] = role
	return s
}

// WithInactiveUser 登记一个存在但非 active 的用户(封禁/锁定)。
func (s *MemoryRoleStore) WithInactiveUser(tenantID, userID int64, role string) *MemoryRoleStore {
	s.roles[[2]int64{tenantID, userID}] = role
	s.inactive[[2]int64{tenantID, userID}] = true
	return s
}

// UserRole 查内存; 不存在 → ErrUserNotFound。
func (s *MemoryRoleStore) UserRole(_ context.Context, tenantID, userID int64) (string, error) {
	role, ok := s.roles[[2]int64{tenantID, userID}]
	if !ok {
		return "", ErrUserNotFound
	}
	return role, nil
}

// ActiveUserRole 仅返回 active 用户的 role; 非 active(WithInactiveUser 登记)→ ErrUserNotFound。
func (s *MemoryRoleStore) ActiveUserRole(_ context.Context, tenantID, userID int64) (string, error) {
	key := [2]int64{tenantID, userID}
	role, ok := s.roles[key]
	if !ok || s.inactive[key] {
		return "", ErrUserNotFound
	}
	return role, nil
}
