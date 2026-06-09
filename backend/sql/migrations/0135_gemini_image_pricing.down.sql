-- revert 0135: drop the seeded Gemini image models from the default pricing version.
BEGIN;
UPDATE billing_pricing_versions
SET pricing_data = jsonb_set(
        pricing_data,
        '{models}',
        (pricing_data->'models') - 'gemini-2.5-flash-image' - 'gemini-3.1-flash-image-preview'
    )
WHERE tenant_id = 0 AND version = '1.0';
COMMIT;
