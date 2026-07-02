package config

// AdminSessionAuthEnabled 报告是否启用 admin session 通道:admin-role 用户 session 直接鉴权
// admin 端点(role 制单登录)。Owner 拍板(2026-07-02)默认开 = 登录即管理员是产品默认形态;
// 显式设 false 可退回纯令牌通道(运维退路)。非法布尔值 fail-loud,不静默取默认。
func AdminSessionAuthEnabled() (bool, error) {
	return envBoolDefault("HUAKAI_ADMIN_SESSION_AUTH_ENABLED", true)
}
