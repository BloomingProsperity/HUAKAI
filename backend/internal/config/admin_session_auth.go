package config

// AdminSessionAuthEnabled 报告是否启用 admin session 通道:admin-role 用户 session 直接鉴权
// admin 端点(role 制单登录迁移)。默认关 → 纯令牌通道,与迁移前逐字同行为。
// 非法布尔值 fail-loud(与其它 *_ENABLED knob 一致),不静默当 false。
func AdminSessionAuthEnabled() (bool, error) {
	return envBool("HUAKAI_ADMIN_SESSION_AUTH_ENABLED")
}
