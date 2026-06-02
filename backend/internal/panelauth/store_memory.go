// HUAKAI · iKun

package panelauth

import "context"

// MemoryRoleStore 测试用内存 role 仓储, key=(tenantID,userID)。
type MemoryRoleStore struct {
	roles map[[2]int64]string
}

// NewMemoryRoleStore 构造空内存仓储。
func NewMemoryRoleStore() *MemoryRoleStore {
	return &MemoryRoleStore{roles: make(map[[2]int64]string)}
}

// WithUser 链式登记一个 (tenant,user) → role。
func (s *MemoryRoleStore) WithUser(tenantID, userID int64, role string) *MemoryRoleStore {
	s.roles[[2]int64{tenantID, userID}] = role
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
