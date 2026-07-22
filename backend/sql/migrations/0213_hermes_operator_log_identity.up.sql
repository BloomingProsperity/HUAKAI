BEGIN;

-- Hermes 日志直接记录真实管理员来源与 ID，不再把操作归到被模拟的普通用户。
ALTER TABLE hermes_audit_events
    ADD COLUMN actor_source TEXT,
    ADD COLUMN actor_id BIGINT,
    ADD COLUMN actor_role TEXT;

UPDATE hermes_audit_events event
SET actor_source = CASE WHEN event.admin_actor_token_id IS NOT NULL THEN 'token' ELSE 'legacy_user' END,
    actor_id = COALESCE(event.admin_actor_token_id, event.actor_user_id),
    actor_role = token.role
FROM admin_tokens token
WHERE event.admin_actor_token_id = token.id;

UPDATE hermes_audit_events
SET actor_source = 'legacy_user',
    actor_id = actor_user_id
WHERE actor_source IS NULL;

ALTER TABLE hermes_audit_events
    ALTER COLUMN actor_source SET NOT NULL,
    ALTER COLUMN actor_id SET NOT NULL,
    ADD CONSTRAINT hermes_audit_events_actor_source_check
        CHECK (actor_source IN ('token', 'session', 'legacy_user')),
    ADD CONSTRAINT hermes_audit_events_actor_id_check
        CHECK (actor_id > 0),
    ADD CONSTRAINT hermes_audit_events_actor_role_check
        CHECK (actor_role IS NULL OR actor_role IN ('platform_admin', 'tenant_operator')),
    DROP COLUMN actor_user_id,
    DROP COLUMN admin_actor_token_id;

CREATE INDEX hermes_audit_events_actor_ts
    ON hermes_audit_events (tenant_id, actor_source, actor_id, ts DESC);

-- 会话创建者同样使用真实管理员身份；owner_user_id 只保留为内部服务主体外键。
ALTER TABLE hermes_conversations
    ADD COLUMN actor_source TEXT,
    ADD COLUMN actor_id BIGINT,
    ADD COLUMN actor_role TEXT;

UPDATE hermes_conversations conversation
SET actor_source = CASE WHEN conversation.admin_actor_token_id IS NOT NULL THEN 'token' ELSE 'legacy_user' END,
    actor_id = COALESCE(conversation.admin_actor_token_id, conversation.owner_user_id),
    actor_role = token.role
FROM admin_tokens token
WHERE conversation.admin_actor_token_id = token.id;

UPDATE hermes_conversations
SET actor_source = 'legacy_user',
    actor_id = owner_user_id
WHERE actor_source IS NULL;

ALTER TABLE hermes_conversations
    ALTER COLUMN actor_source SET NOT NULL,
    ALTER COLUMN actor_id SET NOT NULL,
    ADD CONSTRAINT hermes_conversations_actor_source_check
        CHECK (actor_source IN ('token', 'session', 'legacy_user')),
    ADD CONSTRAINT hermes_conversations_actor_id_check
        CHECK (actor_id > 0),
    ADD CONSTRAINT hermes_conversations_actor_role_check
        CHECK (actor_role IS NULL OR actor_role IN ('platform_admin', 'tenant_operator')),
    DROP COLUMN admin_actor_token_id;

-- 工具日志加入统一日志分类和真实管理员归属，并纳入全局 30 天清理器。
ALTER TABLE hermes_tool_calls
    ADD COLUMN actor_source TEXT,
    ADD COLUMN actor_id BIGINT,
    ADD COLUMN actor_role TEXT,
    ADD COLUMN log_category TEXT NOT NULL DEFAULT 'operation',
    ADD COLUMN ingested_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp();

UPDATE hermes_tool_calls call
SET actor_source = CASE WHEN call.admin_actor_token_id IS NOT NULL THEN 'token' ELSE 'legacy_user' END,
    actor_id = COALESCE(call.admin_actor_token_id, call.actor_user_id),
    actor_role = token.role
FROM admin_tokens token
WHERE call.admin_actor_token_id = token.id;

UPDATE hermes_tool_calls
SET actor_source = 'legacy_user',
    actor_id = actor_user_id
WHERE actor_source IS NULL;

ALTER TABLE hermes_tool_calls
    ALTER COLUMN actor_source SET NOT NULL,
    ALTER COLUMN actor_id SET NOT NULL,
    ADD CONSTRAINT hermes_tool_calls_actor_source_check
        CHECK (actor_source IN ('token', 'session', 'legacy_user')),
    ADD CONSTRAINT hermes_tool_calls_actor_id_check
        CHECK (actor_id > 0),
    ADD CONSTRAINT hermes_tool_calls_actor_role_check
        CHECK (actor_role IS NULL OR actor_role IN ('platform_admin', 'tenant_operator')),
    ADD CONSTRAINT hermes_tool_calls_log_category_check
        CHECK (log_category IN ('operation', 'financial', 'security', 'error', 'access', 'recovery')),
    DROP COLUMN actor_user_id,
    DROP COLUMN admin_actor_token_id;

CREATE INDEX hermes_tool_calls_actor_called_idx
    ON hermes_tool_calls (tenant_id, actor_source, actor_id, called_at DESC);

CREATE INDEX hermes_tool_calls_retention_idx
    ON hermes_tool_calls (ingested_at, id);

COMMIT;
