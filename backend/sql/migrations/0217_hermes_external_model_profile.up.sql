BEGIN;

-- 旧配置依赖 HUAKAI 内部账号池，无法证明可安全映射到新的外部 URL + Key 合同。
-- 项目尚未上线，因此先关闭并清空旧选择，要求管理员重新录入明确的外部模型配置。
UPDATE hermes_settings
SET enabled = FALSE,
    profile_id = NULL,
    updated_at = clock_timestamp();

DELETE FROM hermes_api_profiles;

ALTER TABLE hermes_settings
    DROP CONSTRAINT hermes_settings_source_profile_consistency;

ALTER TABLE hermes_settings
    ALTER COLUMN api_source SET DEFAULT 'external_openai_compatible';

UPDATE hermes_settings
SET api_source = 'external_openai_compatible';

ALTER TABLE hermes_settings
    ADD CONSTRAINT hermes_settings_source_profile_consistency CHECK (
        api_source = 'external_openai_compatible'
        AND (NOT enabled OR profile_id IS NOT NULL)
    );

ALTER TABLE hermes_api_profiles
    DROP CONSTRAINT hermes_api_profiles_kind_group_consistency,
    DROP CONSTRAINT hermes_api_profiles_profile_kind_check,
    DROP CONSTRAINT hermes_api_profiles_tenant_id_pool_group_id_fkey,
    DROP COLUMN pool_group_id;

ALTER TABLE hermes_api_profiles
    ALTER COLUMN profile_kind SET DEFAULT 'external_openai_compatible',
    ADD COLUMN base_url TEXT NOT NULL,
    ADD COLUMN encrypted_api_key BYTEA NOT NULL,
    ADD COLUMN encryption_scheme TEXT NOT NULL,
    ADD COLUMN key_id TEXT NOT NULL,
    ADD COLUMN nonce BYTEA NOT NULL,
    ADD COLUMN aad_hash TEXT NOT NULL,
    ADD COLUMN api_key_fingerprint TEXT NOT NULL,
    ADD COLUMN api_key_hint TEXT NOT NULL,
    ADD COLUMN credential_version INTEGER NOT NULL DEFAULT 1,
    ADD COLUMN secret_binding_id BIGINT NOT NULL;

ALTER TABLE hermes_api_profiles
    ADD CONSTRAINT hermes_api_profiles_kind_check
        CHECK (profile_kind = 'external_openai_compatible'),
    ADD CONSTRAINT hermes_api_profiles_base_url_check
        CHECK (base_url = btrim(base_url) AND char_length(base_url) BETWEEN 8 AND 2048),
    ADD CONSTRAINT hermes_api_profiles_envelope_check
        CHECK (
            octet_length(encrypted_api_key) > 16
            AND encryption_scheme = 'aes-256-gcm'
            AND btrim(key_id) <> ''
            AND octet_length(nonce) = 12
            AND char_length(aad_hash) = 64
            AND char_length(api_key_fingerprint) = 64
            AND char_length(api_key_hint) BETWEEN 4 AND 32
            AND credential_version > 0
            AND secret_binding_id > 0
        ),
    ADD CONSTRAINT hermes_api_profiles_tenant_binding_unique
        UNIQUE (tenant_id, secret_binding_id);

ALTER TABLE hermes_audit_events
    DROP CONSTRAINT hermes_audit_events_action_check,
    ADD CONSTRAINT hermes_audit_events_action_check CHECK (action IN (
        'hermes.enable', 'hermes.disable', 'hermes.profile.create',
        'hermes.profile.rotate', 'hermes.profile.delete',
        'hermes.chat.start', 'hermes.message.send',
        'hermes.conversation.delete',
        'hermes.tool.credential_diagnose', 'hermes.tool.account_health_diagnose',
        'hermes.tool.request_diagnose', 'hermes.tool.dlq_inspect',
        'hermes.tool.audit_lookup', 'hermes.tool.log_analyze'
    ));

COMMIT;
