BEGIN;

WITH bootstrap_pricing AS (
    -- Must match the up migration payload exactly for reversible local rollbacks.
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
),
reset_existing AS (
    UPDATE billing_pricing_versions
    SET pricing_data = '{}'::jsonb,
        created_by_actor = NULL,
        is_public = CASE
            WHEN created_by_actor = 'migration:0068_default_pricing_bootstrap:updated_empty_private_placeholder'
                THEN false
            ELSE true
        END
    WHERE tenant_id = 0
      AND version = '1.0'
      AND pricing_data = (SELECT pricing_data FROM bootstrap_pricing)
      AND created_by_actor IN (
          'migration:0068_default_pricing_bootstrap:updated_empty_private_placeholder',
          'migration:0068_default_pricing_bootstrap:updated_empty_public_placeholder'
      )
    RETURNING id
)
DELETE FROM billing_pricing_versions
WHERE tenant_id = 0
  AND version = '1.0'
  AND pricing_data = (SELECT pricing_data FROM bootstrap_pricing)
  AND created_by_actor = 'migration:0068_default_pricing_bootstrap';

COMMIT;
