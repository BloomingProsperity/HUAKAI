-- Hermes Phase 1 Slice 1 schema gate.
-- Tenant-scoped references use composite FKs following migration 0041.

BEGIN;

ALTER TABLE api_keys
    ADD COLUMN purpose TEXT NOT NULL DEFAULT 'user';

CREATE INDEX IF NOT EXISTS api_keys_purpose_partial
    ON api_keys(purpose) WHERE purpose != 'user';

ALTER TABLE api_keys
    ADD CONSTRAINT api_keys_tenant_user_id_key UNIQUE (tenant_id, user_id, id);

CREATE TABLE hermes_api_profiles (
    id BIGSERIAL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    owner_user_id BIGINT NOT NULL,
    name TEXT NOT NULL,
    profile_kind TEXT NOT NULL CHECK (profile_kind IN ('managed_huakai_api', 'dedicated_group')),
    api_key_id BIGINT,
    pool_group_id BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT hermes_api_profiles_kind_group_consistency CHECK (
        (profile_kind = 'dedicated_group' AND pool_group_id IS NOT NULL)
        OR
        (profile_kind = 'managed_huakai_api' AND pool_group_id IS NULL)
    ),
    CONSTRAINT hermes_api_profiles_tenant_id_id_key UNIQUE (tenant_id, id),
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, owner_user_id) REFERENCES users(tenant_id, id),
    FOREIGN KEY (tenant_id, owner_user_id, api_key_id) REFERENCES api_keys(tenant_id, user_id, id)
        ON DELETE SET NULL (api_key_id),
    FOREIGN KEY (tenant_id, pool_group_id) REFERENCES pool_groups(tenant_id, id)
        ON DELETE SET NULL (pool_group_id)
);

CREATE INDEX hermes_api_profiles_tenant
    ON hermes_api_profiles(tenant_id);
CREATE INDEX hermes_api_profiles_owner
    ON hermes_api_profiles(tenant_id, owner_user_id);

CREATE TABLE hermes_settings (
    tenant_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    api_source TEXT NOT NULL DEFAULT 'managed_huakai_api'
        CHECK (api_source IN ('managed_huakai_api', 'dedicated_group')),
    profile_id BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, user_id),
    CONSTRAINT hermes_settings_source_profile_consistency CHECK (
        (api_source = 'managed_huakai_api')
        OR
        (api_source = 'dedicated_group' AND profile_id IS NOT NULL)
    ),
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, user_id) REFERENCES users(tenant_id, id),
    FOREIGN KEY (tenant_id, profile_id) REFERENCES hermes_api_profiles(tenant_id, id)
        ON DELETE SET NULL (profile_id)
);

CREATE TABLE hermes_audit_events (
    id BIGSERIAL PRIMARY KEY,
    ts TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    tenant_id BIGINT NOT NULL,
    actor_user_id BIGINT NOT NULL,
    action TEXT NOT NULL CHECK (action IN (
        'hermes.enable',
        'hermes.disable',
        'hermes.profile.create',
        'hermes.profile.rotate',
        'hermes.chat.start'
    )),
    sanitized_args JSONB,
    result TEXT NOT NULL CHECK (result IN ('success', 'failure')),
    correlation_id TEXT,
    request_id TEXT,
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, actor_user_id) REFERENCES users(tenant_id, id)
        ON DELETE RESTRICT
);

CREATE INDEX hermes_audit_events_tenant_ts
    ON hermes_audit_events(tenant_id, ts DESC);
CREATE INDEX hermes_audit_events_correlation
    ON hermes_audit_events(tenant_id, correlation_id) WHERE correlation_id IS NOT NULL;

COMMIT;
