BEGIN;

CREATE TABLE IF NOT EXISTS api_key_groups (
    id          bigserial PRIMARY KEY,
    tenant_id   bigint NOT NULL REFERENCES tenants(id),
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    enabled     boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz,
    CONSTRAINT uq_api_key_groups_tenant_id_id UNIQUE (tenant_id, id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_api_key_groups_tenant_name
    ON api_key_groups (tenant_id, name)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_api_key_groups_tenant_enabled
    ON api_key_groups (tenant_id, enabled)
    WHERE deleted_at IS NULL;

ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS key_group_id bigint,
    ADD COLUMN IF NOT EXISTS quota_policy_id bigint;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'fk_api_keys_key_group'
    ) THEN
        ALTER TABLE api_keys
            ADD CONSTRAINT fk_api_keys_key_group
            FOREIGN KEY (tenant_id, key_group_id)
            REFERENCES api_key_groups (tenant_id, id)
            DEFERRABLE INITIALLY DEFERRED;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'fk_api_keys_quota_policy'
    ) THEN
        ALTER TABLE api_keys
            ADD CONSTRAINT fk_api_keys_quota_policy
            FOREIGN KEY (tenant_id, quota_policy_id)
            REFERENCES quota_policies (tenant_id, id)
            DEFERRABLE INITIALLY DEFERRED;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_api_keys_group
    ON api_keys (tenant_id, key_group_id)
    WHERE deleted_at IS NULL AND key_group_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_api_keys_quota_policy
    ON api_keys (tenant_id, quota_policy_id)
    WHERE deleted_at IS NULL AND quota_policy_id IS NOT NULL;

COMMENT ON TABLE api_key_groups IS
    '0079: tenant-scoped API key groups. No bearer credential material.';

COMMIT;
