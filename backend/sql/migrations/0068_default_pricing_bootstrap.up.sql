BEGIN;

WITH bootstrap_pricing AS (
    -- Seed only the smoke setup model; unpriced models must continue to fail closed.
    SELECT $pricing$
{
  "models": {
    "gpt-4.1-mini": {
      "input_micro_usd": 0.40,
      "output_micro_usd": 1.60,
      "cache_read_micro_usd": 0.10
    }
  }
}
$pricing$::jsonb AS pricing_data
)
INSERT INTO billing_pricing_versions (
    tenant_id,
    version,
    pricing_data,
    effective_from,
    created_by_actor,
    is_public
)
SELECT
    0,
    '1.0',
    pricing_data,
    '2026-05-18T00:00:00Z'::timestamptz,
    'migration:0068_default_pricing_bootstrap',
    true
FROM bootstrap_pricing
ON CONFLICT (tenant_id, version) DO UPDATE
SET pricing_data = EXCLUDED.pricing_data,
    is_public = true,
    created_by_actor = CASE
        WHEN billing_pricing_versions.is_public
            THEN 'migration:0068_default_pricing_bootstrap:updated_empty_public_placeholder'
        ELSE 'migration:0068_default_pricing_bootstrap:updated_empty_private_placeholder'
    END
WHERE billing_pricing_versions.pricing_data = '{}'::jsonb
  AND billing_pricing_versions.created_by_actor IS NULL;

COMMIT;
