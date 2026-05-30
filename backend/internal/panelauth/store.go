// HUAKAI · iKun

package panelauth

import "context"

// RoleStore 读取 users.role(租户内, 未软删)。
type RoleStore interface {
	UserRole(ctx context.Context, tenantID, userID int64) (string, error)
}
