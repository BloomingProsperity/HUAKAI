BEGIN;

CREATE TEMP TABLE hermes_service_principal_drop_ids (
    tenant_id BIGINT,
    user_id BIGINT,
    api_key_id BIGINT
) ON COMMIT DROP;

INSERT INTO hermes_service_principal_drop_ids (tenant_id, user_id, api_key_id)
SELECT tenant_id, user_id, api_key_id
FROM hermes_service_principals;

DROP TRIGGER IF EXISTS protect_service_api_key ON api_keys;
DROP FUNCTION IF EXISTS huakai_protect_service_api_key();
DROP TRIGGER IF EXISTS protect_service_user ON users;
DROP FUNCTION IF EXISTS huakai_protect_service_user();

DROP TABLE hermes_service_principals;

DELETE FROM hermes_conversations
WHERE (tenant_id, owner_user_id) IN (
    SELECT tenant_id, user_id FROM hermes_service_principal_drop_ids
);

DELETE FROM hermes_settings
WHERE (tenant_id, user_id) IN (
    SELECT tenant_id, user_id FROM hermes_service_principal_drop_ids
);

DELETE FROM hermes_api_profiles
WHERE (tenant_id, owner_user_id) IN (
    SELECT tenant_id, user_id FROM hermes_service_principal_drop_ids
);

-- 0213 down 会把新日志重新归属到服务主体。0212 删除主体前必须同步清理这些
-- 仅属于已撤销 Hermes 身份模型的日志，既避免审计外键阻断回滚，也不留下孤儿工具日志。
DELETE FROM hermes_tool_calls
WHERE (tenant_id, actor_user_id) IN (
    SELECT tenant_id, user_id FROM hermes_service_principal_drop_ids
);

DELETE FROM hermes_audit_events
WHERE (tenant_id, actor_user_id) IN (
    SELECT tenant_id, user_id FROM hermes_service_principal_drop_ids
);

ALTER TABLE hermes_settings
    DROP CONSTRAINT hermes_settings_enabled_model_check,
    DROP CONSTRAINT hermes_settings_source_profile_consistency,
    DROP COLUMN model_key;

ALTER TABLE hermes_settings
    ADD CONSTRAINT hermes_settings_source_profile_consistency CHECK (
        (api_source = 'managed_huakai_api')
        OR
        (api_source = 'dedicated_group' AND profile_id IS NOT NULL)
    );

ALTER TABLE hermes_api_profiles
    ADD COLUMN api_key_id BIGINT;

ALTER TABLE hermes_api_profiles
    ADD CONSTRAINT hermes_api_profiles_tenant_id_owner_user_id_api_key_id_fkey
        FOREIGN KEY (tenant_id, owner_user_id, api_key_id)
        REFERENCES api_keys(tenant_id, user_id, id)
        ON DELETE SET NULL (api_key_id);

DELETE FROM api_keys
WHERE id IN (SELECT api_key_id FROM hermes_service_principal_drop_ids);

DELETE FROM users
WHERE id IN (SELECT user_id FROM hermes_service_principal_drop_ids);

DELETE FROM tenant_admin_capability_grants
WHERE capability = 'hermes_operations';

ALTER TABLE tenant_admin_capability_grants
    DROP CONSTRAINT tenant_admin_capability_grants_capability_check;

ALTER TABLE tenant_admin_capability_grants
    ADD CONSTRAINT tenant_admin_capability_grants_capability_check
        CHECK (capability IN ('advanced_account_intake'));

ALTER TABLE api_keys
    DROP CONSTRAINT api_keys_purpose_check;

ALTER TABLE users
    DROP CONSTRAINT users_principal_kind_check,
    DROP COLUMN principal_kind;

COMMIT;
