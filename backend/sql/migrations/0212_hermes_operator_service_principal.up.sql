BEGIN;

-- 普通用户和内部服务主体共用 users/api_keys 的外键骨架，但身份性质必须显式区分。
-- 服务主体不是第四种人类角色，没有登录凭据，也不能被用户管理、余额分发或公开 Key 路径操作。
ALTER TABLE users
    ADD COLUMN principal_kind TEXT NOT NULL DEFAULT 'human';

ALTER TABLE users
    ADD CONSTRAINT users_principal_kind_check
        CHECK (principal_kind IN ('human', 'service'));

ALTER TABLE api_keys
    ADD CONSTRAINT api_keys_purpose_check
        CHECK (purpose IN ('user', 'hermes'));

CREATE TABLE hermes_service_principals (
    tenant_id  BIGINT PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    user_id    BIGINT NOT NULL,
    api_key_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (tenant_id, user_id),
    UNIQUE (tenant_id, api_key_id),
    FOREIGN KEY (tenant_id, user_id)
        REFERENCES users(tenant_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, user_id, api_key_id)
        REFERENCES api_keys(tenant_id, user_id, id) ON DELETE RESTRICT
);

COMMENT ON TABLE hermes_service_principals IS
    'Hermes 内部模型调用的租户级服务主体映射；不代表普通用户，不持有可用明文 bearer。';

-- 旧 profile 的 api_key_id 来自“让 Hermes 直接持有用户 Key”的早期方案。
-- 正式链统一使用上面的内部服务主体进入网关，因此删除该平行凭据入口。
ALTER TABLE hermes_api_profiles
    DROP COLUMN api_key_id CASCADE;

-- 模型选择属于管理员配置，不信任 runner 或聊天请求临时指定。
-- 旧的启用记录没有可证明的模型配置，迁移时先关闭，等待管理员明确选择后再启用。
ALTER TABLE hermes_settings
    ADD COLUMN model_key TEXT NOT NULL DEFAULT '';

UPDATE hermes_settings
SET enabled = FALSE,
    updated_at = clock_timestamp()
WHERE enabled = TRUE;

ALTER TABLE hermes_settings
    DROP CONSTRAINT hermes_settings_source_profile_consistency;

ALTER TABLE hermes_settings
    ADD CONSTRAINT hermes_settings_source_profile_consistency CHECK (
        (api_source = 'managed_huakai_api' AND profile_id IS NULL)
        OR
        (api_source = 'dedicated_group' AND profile_id IS NOT NULL)
    ),
    ADD CONSTRAINT hermes_settings_enabled_model_check CHECK (
        NOT enabled OR (btrim(model_key) <> '' AND char_length(model_key) <= 255)
    );

-- 部署者可以把 Hermes 运维能力授予某个下级租户管理员；缺失记录仍按未授权处理。
ALTER TABLE tenant_admin_capability_grants
    DROP CONSTRAINT tenant_admin_capability_grants_capability_check;

ALTER TABLE tenant_admin_capability_grants
    ADD CONSTRAINT tenant_admin_capability_grants_capability_check
        CHECK (capability IN ('advanced_account_intake', 'hermes_operations'));

-- 即使内部 ID 被猜到，通用用户/Key 管理 SQL 也不能改变仍受映射管理的服务主体。
-- 内部维护流程需要先删除映射，再按 Key、用户的顺序回收父行；这样既不会留下不可清理的孤儿，
-- 也不会给普通管理接口留下绕过入口。
CREATE OR REPLACE FUNCTION huakai_protect_service_user()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.principal_kind = 'service'
       AND EXISTS (
           SELECT 1
           FROM hermes_service_principals
           WHERE tenant_id = OLD.tenant_id
             AND user_id = OLD.id
       ) THEN
        RAISE EXCEPTION 'service principal user is managed internally'
            USING ERRCODE = '42501';
    END IF;
    RETURN COALESCE(NEW, OLD);
END;
$$;

CREATE TRIGGER protect_service_user
BEFORE UPDATE OR DELETE ON users
FOR EACH ROW EXECUTE FUNCTION huakai_protect_service_user();

CREATE OR REPLACE FUNCTION huakai_protect_service_api_key()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.purpose = 'hermes'
       AND EXISTS (
           SELECT 1
           FROM hermes_service_principals
           WHERE tenant_id = OLD.tenant_id
             AND api_key_id = OLD.id
       ) THEN
        RAISE EXCEPTION 'service principal api key is managed internally'
            USING ERRCODE = '42501';
    END IF;
    RETURN COALESCE(NEW, OLD);
END;
$$;

CREATE TRIGGER protect_service_api_key
BEFORE UPDATE OR DELETE ON api_keys
FOR EACH ROW EXECUTE FUNCTION huakai_protect_service_api_key();

COMMIT;
