BEGIN;

CREATE TABLE IF NOT EXISTS pool_group_pricing_ratios (
    id            bigserial     PRIMARY KEY,
    tenant_id     bigint        NOT NULL REFERENCES tenants(id),
    pool_group_id bigint        NOT NULL,
    ratio         numeric(20,8) NOT NULL CHECK (ratio > 0),
    public_ratio  boolean       NOT NULL DEFAULT false,
    created_by    text          NOT NULL,
    updated_by    text          NOT NULL,
    created_at    timestamptz   NOT NULL DEFAULT now(),
    updated_at    timestamptz   NOT NULL DEFAULT now(),
    CONSTRAINT pool_group_pricing_ratios_pool_group_id_fkey
        FOREIGN KEY (tenant_id, pool_group_id) REFERENCES pool_groups(tenant_id, id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_pgpr_tenant_group
    ON pool_group_pricing_ratios (tenant_id, pool_group_id);

CREATE INDEX IF NOT EXISTS idx_pgpr_tenant
    ON pool_group_pricing_ratios (tenant_id);

COMMENT ON TABLE pool_group_pricing_ratios IS
    'Per pool-group pricing ratio multiplier. effective_price = base_price * ratio. '
    'ratio > 0 enforced by CHECK. public_ratio controls whether multiplier is visible to end-users.';

COMMIT;
