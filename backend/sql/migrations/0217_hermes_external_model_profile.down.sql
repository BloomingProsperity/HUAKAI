BEGIN;

UPDATE hermes_settings
SET enabled = FALSE,
    profile_id = NULL,
    updated_at = clock_timestamp();

DELETE FROM hermes_api_profiles;

ALTER TABLE hermes_settings
    DROP CONSTRAINT hermes_settings_source_profile_consistency;

ALTER TABLE hermes_settings
    ALTER COLUMN api_source SET DEFAULT 'managed_huakai_api';

UPDATE hermes_settings
SET api_source = 'managed_huakai_api';

ALTER TABLE hermes_settings
    ADD CONSTRAINT hermes_settings_source_profile_consistency CHECK (
        (api_source = 'managed_huakai_api' AND profile_id IS NULL)
        OR
        (api_source = 'dedicated_group' AND profile_id IS NOT NULL)
    );

ALTER TABLE hermes_api_profiles
    DROP CONSTRAINT hermes_api_profiles_tenant_binding_unique,
    DROP CONSTRAINT hermes_api_profiles_envelope_check,
    DROP CONSTRAINT hermes_api_profiles_base_url_check,
    DROP CONSTRAINT hermes_api_profiles_kind_check,
    DROP COLUMN secret_binding_id,
    DROP COLUMN credential_version,
    DROP COLUMN api_key_hint,
    DROP COLUMN api_key_fingerprint,
    DROP COLUMN aad_hash,
    DROP COLUMN nonce,
    DROP COLUMN key_id,
    DROP COLUMN encryption_scheme,
    DROP COLUMN encrypted_api_key,
    DROP COLUMN base_url,
    ADD COLUMN pool_group_id BIGINT;

ALTER TABLE hermes_api_profiles
    ALTER COLUMN profile_kind DROP DEFAULT,
    ADD CONSTRAINT hermes_api_profiles_profile_kind_check
        CHECK (profile_kind IN ('managed_huakai_api', 'dedicated_group')),
    ADD CONSTRAINT hermes_api_profiles_kind_group_consistency CHECK (
        (profile_kind = 'dedicated_group' AND pool_group_id IS NOT NULL)
        OR
        (profile_kind = 'managed_huakai_api' AND pool_group_id IS NULL)
    ),
    ADD CONSTRAINT hermes_api_profiles_tenant_id_pool_group_id_fkey
        FOREIGN KEY (tenant_id, pool_group_id)
        REFERENCES pool_groups(tenant_id, id)
        ON DELETE SET NULL (pool_group_id);

ALTER TABLE hermes_audit_events
    DROP CONSTRAINT hermes_audit_events_action_check,
    ADD CONSTRAINT hermes_audit_events_action_check CHECK (action IN (
        'hermes.enable', 'hermes.disable', 'hermes.profile.create',
        'hermes.profile.rotate', 'hermes.chat.start', 'hermes.message.send',
        'hermes.conversation.delete',
        'hermes.tool.credential_diagnose', 'hermes.tool.account_health_diagnose',
        'hermes.tool.request_diagnose', 'hermes.tool.dlq_inspect',
        'hermes.tool.audit_lookup', 'hermes.tool.log_analyze'
    ));

COMMIT;
