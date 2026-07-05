// HUAKAI · iKun

package panelauth

import "context"

// RoleStore 读取 users.role(租户内, 未软删)。
type RoleStore interface {
	UserRole(ctx context.Context, tenantID, userID int64) (string, error)
	// ActiveUserRole 仅返回 status='active' 行的 role; 非 active(封禁/锁定/待重置)→ ErrUserNotFound。
	ActiveUserRole(ctx context.Context, tenantID, userID int64) (string, error)
}
