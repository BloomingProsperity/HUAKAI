BEGIN;

-- 确认值只向管理员返回一次，数据库仅保存哈希。DELETE RETURNING 保证任意网关副本中
-- 只有一个确认请求能成功消费，错误管理员或错误工具命中时也会立即销毁该值。
CREATE TABLE hermes_pending_confirmations (
    token_hash   BYTEA PRIMARY KEY,
    tool_name    TEXT NOT NULL,
    tenant_id    BIGINT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    actor_source TEXT NOT NULL,
    actor_id     BIGINT NOT NULL,
    target_id    BIGINT NOT NULL,
    args_binding_hash BYTEA NOT NULL,
    plan_binding_hash BYTEA NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT hermes_pending_confirmations_hash_check CHECK (octet_length(token_hash) = 32),
    CONSTRAINT hermes_pending_confirmations_tool_check CHECK (btrim(tool_name) <> '' AND char_length(tool_name) <= 128),
    CONSTRAINT hermes_pending_confirmations_actor_source_check CHECK (actor_source IN ('token', 'session')),
    CONSTRAINT hermes_pending_confirmations_actor_check CHECK (actor_id > 0),
    CONSTRAINT hermes_pending_confirmations_target_check CHECK (target_id > 0),
    CONSTRAINT hermes_pending_confirmations_args_hash_check CHECK (octet_length(args_binding_hash) = 32),
    CONSTRAINT hermes_pending_confirmations_plan_hash_check CHECK (octet_length(plan_binding_hash) = 32),
    CONSTRAINT hermes_pending_confirmations_expiry_check CHECK (expires_at > created_at)
);

CREATE INDEX hermes_pending_confirmations_expiry_idx
    ON hermes_pending_confirmations (expires_at);

COMMIT;
