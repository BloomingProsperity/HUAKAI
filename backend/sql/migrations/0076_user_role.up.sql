-- HUAKAI 角色面板切换: users 表增加 role 列。
-- 二分角色: 'admin' 登录后进管理面板, 'user' 进用户面板(员工亦为 user)。
-- 既有 admin_tokens(hk_admin programmatic 凭据)保持独立;
-- 本列只服务「账号登录 → 面板归属」, 与 admin_tokens.role(platform_admin/tenant_operator)是不同维度。
BEGIN;

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS role text NOT NULL DEFAULT 'user'
        CHECK (role IN ('admin', 'user'));

COMMENT ON COLUMN users.role IS
    '面板角色(S1a, 2026-05-30): admin→管理面板, user→用户面板(员工亦为 user)。'
    '与 admin_tokens.role 不同维度: 此列管「人登录的面板归属」, admin_tokens 管「程序化 admin API 凭据」。'
    '默认 user 即 deny-by-default(新账号绝不自动获得 admin 面板)。';

COMMIT;
