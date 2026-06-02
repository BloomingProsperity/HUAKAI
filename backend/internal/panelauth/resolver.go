// HUAKAI · iKun

package panelauth

import "context"

// Resolver 把「租户 + 用户」解析为面板归属(查 users.role → PanelForRole)。
type Resolver struct {
	store RoleStore
}

// NewResolver 构造解析器。
func NewResolver(store RoleStore) *Resolver {
	return &Resolver{store: store}
}

// PanelForUser 查该用户的 role 并映射面板。用户不存在 → ErrUserNotFound;
// store 未注入 → ErrStoreNotConfigured。
func (r *Resolver) PanelForUser(ctx context.Context, tenantID, userID int64) (Panel, error) {
	if r == nil || r.store == nil {
		return PanelNone, ErrStoreNotConfigured
	}
	role, err := r.store.UserRole(ctx, tenantID, userID)
	if err != nil {
		return PanelNone, err
	}
	return PanelForRole(role), nil
}
