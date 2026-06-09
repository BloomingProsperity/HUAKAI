-- revert 0132: drop the seeded MiniMax model keys from the default pricing version.
BEGIN;
UPDATE billing_pricing_versions
SET pricing_data = jsonb_set(
        pricing_data,
        '{models}',
        (pricing_data->'models') - 'MiniMax-M2' - 'MiniMax-M2.1' - 'MiniMax-M2.7' - 'MiniMax-M3'
    )
WHERE tenant_id = 0 AND version = '1.0';
COMMIT;
