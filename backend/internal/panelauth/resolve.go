// HUAKAI · iKun

package panelauth

// PanelForRole 由 users.role 映射面板归属。二分 + deny-by-default:
// 仅精确等于 'admin' → 管理面板; 其余一切(含 'user' / 空串 / 大小写不符 / 未知值 / 未来新增值)
// → 用户面板。这样 role 字段被污染、迁移遗留、或将来扩展都不会误授管理面板(越权安全默认)。
func PanelForRole(role string) Panel {
	if role == RoleAdmin {
		return PanelAdmin
	}
	return PanelUser
}

// PanelForAdminToken 持有效 hk_admin token 的主体直接进管理面板
// (保留既有 admin 凭据世界, 隔离不动; 此分支由调用方在已验证 admin token 后调用)。
func PanelForAdminToken() Panel {
	return PanelAdmin
}
