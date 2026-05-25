-- codex review HEAD chunk7 P1#5: session_tokens / refresh_tokens / user
-- 邀请 binding 用单列 UUID 或 text FK 指 session_families.id / invite_codes.code,
-- tenant_id 维度未绑定。child 行 declare tenant A 但 family/invite 属 tenant B
-- 可成立, DR-001 跨表 invariant 在 DB 层失守。

BEGIN;

-- session_families 加 UNIQUE (tenant_id, id) 作为 composite FK 目标。
ALTER TABLE session_families
    ADD CONSTRAINT session_families_tenant_id_id_key UNIQUE (tenant_id, id);
ALTER TABLE invite_codes
    ADD CONSTRAINT invite_codes_tenant_id_code_key UNIQUE (tenant_id, code);

-- session_tokens.family_id → session_families (tenant_id, id) 复合
ALTER TABLE session_tokens
    DROP CONSTRAINT IF EXISTS session_tokens_family_id_fkey;
ALTER TABLE session_tokens
    ADD CONSTRAINT session_tokens_family_id_fkey
    FOREIGN KEY (tenant_id, family_id) REFERENCES session_families(tenant_id, id) ON DELETE CASCADE;

-- refresh_tokens.family_id → session_families (tenant_id, id) 复合
ALTER TABLE refresh_tokens
    DROP CONSTRAINT IF EXISTS refresh_tokens_family_id_fkey;
ALTER TABLE refresh_tokens
    ADD CONSTRAINT refresh_tokens_family_id_fkey
    FOREIGN KEY (tenant_id, family_id) REFERENCES session_families(tenant_id, id) ON DELETE CASCADE;

-- invite_bindings (邀请 binding 表) 已有 (tenant_id, user_id) → users 复合 FK;
-- 单独 (invite_code) → invite_codes(code) 加 tenant_id 维度。
ALTER TABLE invite_bindings
    DROP CONSTRAINT IF EXISTS invite_bindings_invite_code_fkey;
ALTER TABLE invite_bindings
    ADD CONSTRAINT invite_bindings_invite_code_fkey
    FOREIGN KEY (tenant_id, invite_code) REFERENCES invite_codes(tenant_id, code);

COMMIT;
