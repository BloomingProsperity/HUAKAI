-- revert 0133: drop the seeded OpenAI DALL-E image models from the default pricing
-- version. Surgical (removes only the dall-e keys) so any other providers.openai
-- content is preserved; an empty providers scaffold, if left, is harmless.
BEGIN;
UPDATE billing_pricing_versions
SET pricing_data = jsonb_set(
        pricing_data,
        '{providers,openai,models}',
        (pricing_data#>'{providers,openai,models}') - 'dall-e-3' - 'dall-e-2')
WHERE tenant_id = 0 AND version = '1.0'
  AND pricing_data #> '{providers,openai,models}' IS NOT NULL;
COMMIT;
