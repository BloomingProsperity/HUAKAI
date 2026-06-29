-- 0163_device_confirmations.up.sql
--
-- 新设备确认流 (default-dormant)。当运营者把 DevicePolicy 设为 'confirm' 且某用户的活跃登录设备
-- (session_families) 达到 MaxActiveFamilies 上限时, 登录不再裸失败, 而是先在本表落一条 pending
-- 记录 (存设备上下文 + token_hash + 过期), 给用户发确认邮件; 用户带原文 token 调确认端点校验通过后,
-- 撤掉最老 family 腾位, 再次登录即成功。
--
-- 红线: 本迁移 additive only —— 只建新表 + 新索引, 绝不动 session_families / session_tokens /
--   refresh_tokens 的任何现有约束。本表只存 token_hash (sha256), 永不存原文 token。
-- FK 风格对照 0021 (session 表): tenant_id REFERENCES tenants(id), (tenant_id,user_id) 复合 FK 指向 users。

BEGIN;

CREATE TABLE IF NOT EXISTS device_confirmations (
    id              bigserial   PRIMARY KEY,
    tenant_id       bigint      NOT NULL REFERENCES tenants(id),
    user_id         bigint      NOT NULL,
    -- 只存 sha256(token), 原文 token 仅经邮件交付给用户, 永不落库。
    token_hash      bytea       NOT NULL,
    -- 触发确认时的设备上下文快照 (供运维审计 / 用户辨识), object 形态。
    device_info     jsonb       NOT NULL DEFAULT '{}'::jsonb,
    ip              text        NOT NULL DEFAULT '',
    user_agent      text        NOT NULL DEFAULT '',
    status          text        NOT NULL DEFAULT 'pending'
                                CHECK (status IN ('pending', 'confirmed', 'expired')),
    created_at      timestamptz NOT NULL,
    expires_at      timestamptz NOT NULL,
    confirmed_at    timestamptz,
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id),
    CONSTRAINT device_confirmations_device_info_object CHECK (jsonb_typeof(device_info) = 'object')
);

-- 唯一索引: 同租户 token_hash 全局唯一 (生成碰撞概率为零, 此约束防误重复插)。
CREATE UNIQUE INDEX IF NOT EXISTS uq_device_confirmations_token_hash
    ON device_confirmations (tenant_id, token_hash);

-- 反查: 按用户 + 状态列待确认记录 (运维 / 未来清理 worker)。
CREATE INDEX IF NOT EXISTS idx_device_confirmations_user_status
    ON device_confirmations (tenant_id, user_id, status);

COMMENT ON TABLE device_confirmations IS
    '新设备确认 pending 记录。DevicePolicy=confirm 达上限时落一条; 存 token_hash only, 原文 token 仅经邮件交付。';

COMMIT;
