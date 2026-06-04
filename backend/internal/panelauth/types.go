// HUAKAI · iKun

// Package panelauth 计算「已登录主体 → 面板归属」(管理面板 / 用户面板)。
// 角色面板切换 S1a 数据/解析层(休眠, 无 HTTP/wiring)。二分模型:
// users.role='admin' → 管理面板; 其余一切(含 'user'/空/未知)→ 用户面板(deny-by-default,
// 绝不因角色缺失或异常而误授管理面板)。既有 admin_tokens(hk_admin)是另一维度,
// 由 admin.AdminResolver 处理; 本包只管「账号登录」这条线的面板归属, 不碰 admin 凭据世界。
package panelauth

import "errors"

// Panel 是登录后应进入的面板。
type Panel string

const (
	// PanelAdmin 管理面板(仅 role=admin 账号, 或持有效 hk_admin token 的主体)。
	PanelAdmin Panel = "admin"
	// PanelUser 普通用户面板(员工 / 普通用户)。
	PanelUser Panel = "user"
	// PanelNone 无面板(未认证)。
	PanelNone Panel = "none"
)

const (
	// RoleAdmin users.role 的管理员值。
	RoleAdmin = "admin"
	// RoleUser users.role 的普通用户值(列默认)。
	RoleUser = "user"
)

var (
	// ErrStoreNotConfigured 表示 role store 未注入(nil 安全防御)。
	ErrStoreNotConfigured = errors.New("panelauth: role store not configured")
	// ErrUserNotFound 表示该租户下无此 user(或已软删)。
	ErrUserNotFound = errors.New("panelauth: user not found for tenant")
)
