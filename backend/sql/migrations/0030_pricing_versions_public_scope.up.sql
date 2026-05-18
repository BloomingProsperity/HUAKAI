BEGIN;

INSERT INTO tenants (id, name, status, created_at, updated_at)
VALUES (0, 'public-pricing', 'active', now(), now())
ON CONFLICT (id) DO NOTHING;

INSERT INTO billing_pricing_versions (
    tenant_id,
    version,
    pricing_data,
    effective_from,
    created_by_actor
)
VALUES (
    0,
    'public_default_v1',
    '{}'::jsonb,
    '2026-05-18T00:00:00Z'::timestamptz,
    'migration:0030_public_pricing_scope'
)
ON CONFLICT (tenant_id, version) DO NOTHING;

COMMIT;
