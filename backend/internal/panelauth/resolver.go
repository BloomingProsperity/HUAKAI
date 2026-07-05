// HUAKAI · iKun

package panelauth

import (
	"context"
	"errors"
)

// Resolver 把「租户 + 用户」解析为面板归属(查 users.role → PanelForRole)。
type Resolver struct {
	store RoleStore
}

// NewResolver 构造解析器。
func NewResolver(store RoleStore) *Resolver {
	return &Resolver{store: store}
}

// PanelForUser 查该用户的 role 并映射面板, 让 /me 的面板归属反映账号资格:
//   - active → 按 role 映射(admin→admin, 其余→user);
//   - 存在但非 active(封禁/锁定/待重置)→ 降级为 user 面板, 绝不给 admin 面板
//     (与 admin 权力面的 ActiveUserRole 口径一致); 不 403 是为不把锁定态用户
//     踢出面板(锁定只守登录门, 见会话资格门决策), 避免失败锁定被当面板 DoS;
//   - 已删/不存在 → ErrUserNotFound(handler 映 403 account_not_active)。
//
// store 未注入 → ErrStoreNotConfigured。
func (r *Resolver) PanelForUser(ctx context.Context, tenantID, userID int64) (Panel, error) {
	if r == nil || r.store == nil {
		return PanelNone, ErrStoreNotConfigured
	}
	role, err := r.store.ActiveUserRole(ctx, tenantID, userID)
	if err == nil {
		return PanelForRole(role), nil
	}
	if !errors.Is(err, ErrUserNotFound) {
		return PanelNone, err
	}
	// 非 active: 区分"已删/不存在"(拒)与"存在但被封/锁"(降级到 user 面板)。
	if _, roleErr := r.store.UserRole(ctx, tenantID, userID); roleErr != nil {
		return PanelNone, roleErr
	}
	return PanelUser, nil
}
