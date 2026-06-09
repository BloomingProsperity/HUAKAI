-- revert 0134: drop the seeded OpenAI gpt-image models from the default pricing
-- version. Surgical (removes only the gpt-image keys); any other
-- providers.openai.models content (e.g. the DALL-E seed from 0133) is preserved.
BEGIN;
UPDATE billing_pricing_versions
SET pricing_data = jsonb_set(
        pricing_data,
        '{providers,openai,models}',
        (pricing_data#>'{providers,openai,models}') - 'gpt-image-1' - 'gpt-image-1.5')
WHERE tenant_id = 0 AND version = '1.0'
  AND pricing_data #> '{providers,openai,models}' IS NOT NULL;
COMMIT;
