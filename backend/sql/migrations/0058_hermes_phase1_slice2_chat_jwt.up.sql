-- Hermes Phase 1 Slice 2 chat + JWT schema gate.
-- Builds on 0057 core (settings + api_profiles + audit_events).
-- 沿 0041 composite tenant FK 防错绑.

BEGIN;

-- Part A: hermes_conversations
-- 用户 chat 会话元信息. Slice 2 messages 表的父表.
CREATE TABLE hermes_conversations (
    id BIGSERIAL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    owner_user_id BIGINT NOT NULL,
    title TEXT,                       -- nullable; 默认 hermes-agent 第一条 user msg 截断生成
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_message_at TIMESTAMPTZ,      -- nullable; 新 conv 还未发 msg 时为 NULL
    deleted_at TIMESTAMPTZ,           -- soft delete (Slice 2.5 cleanup 时考虑)
    CONSTRAINT hermes_conversations_tenant_id_id_key UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, owner_user_id) REFERENCES users(tenant_id, id)
);

CREATE INDEX hermes_conversations_owner_active
    ON hermes_conversations(tenant_id, owner_user_id, last_message_at DESC)
    WHERE deleted_at IS NULL;

-- Part B: hermes_messages
-- conv 内消息历史, append-only. persist-on-event:done 模型 (D5).
CREATE TABLE hermes_messages (
    id BIGSERIAL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    conversation_id BIGINT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'system', 'tool')),
    content JSONB NOT NULL,           -- {"type":"text","text":"..."} 或 multimodal
    token_count INTEGER,              -- nullable; assistant message 才有 (stream 完成时填)
    completed_at TIMESTAMPTZ,         -- nullable; assistant message stream 完成时填
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, conversation_id) REFERENCES hermes_conversations(tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX hermes_messages_conv_created
    ON hermes_messages(tenant_id, conversation_id, created_at);

-- Part C: hermes_jwt_keys
-- JWT public key registry 支持 hot rotation. 私钥 file mount, 公钥进 DB.
CREATE TABLE hermes_jwt_keys (
    kid TEXT PRIMARY KEY,             -- key ID (JWT header kid claim); 用 timestamp + short hash
    alg TEXT NOT NULL CHECK (alg IN ('EdDSA', 'ES256', 'RS256')), -- whitelist
    public_key_pem TEXT NOT NULL,
    valid_from TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    valid_until TIMESTAMPTZ,          -- nullable = no expiry (active key)
    revoked_at TIMESTAMPTZ,           -- emergency 撤销
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- PostgreSQL partial index predicate 不能使用 NOW(); 时间窗由查询条件判定.
CREATE INDEX hermes_jwt_keys_active
    ON hermes_jwt_keys(valid_from, valid_until)
    WHERE revoked_at IS NULL;

-- Part D: 加 hermes_audit_events.action CHECK 'hermes.message.send'
-- 沿用 0057 audit_events 表 ALTER 加新 action.
ALTER TABLE hermes_audit_events
    DROP CONSTRAINT IF EXISTS hermes_audit_events_action_check;

ALTER TABLE hermes_audit_events
    ADD CONSTRAINT hermes_audit_events_action_check
        CHECK (action IN (
            'hermes.enable',
            'hermes.disable',
            'hermes.profile.create',
            'hermes.profile.rotate',
            'hermes.chat.start',
            'hermes.message.send'     -- Slice 2 加
        ));

COMMIT;
