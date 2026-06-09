-- 0135: seed official Gemini image-generation prices into the default pricing
-- version (tenant 0, '1.0'). These models generate images via the generateContent
-- (chat) path -- the image is returned as an inlineData content part and the image
-- tokens are counted in candidatesTokenCount (OutputTokens), so they bill through
-- the normal chat token scheme under the top-level "models" map. The output rate is
-- the image-output token rate (an image model's output is the image); this matches
-- new-api, which sets an image model's output ratio to the image rate. Verified vs
-- ai.google.dev/gemini-api/docs/pricing (each value is USD per 1M tokens):
--   gemini-2.5-flash-image          text input $0.30/1M, image output $30/1M
--                                   ($0.039/image at ~1290 tokens)
--   gemini-3.1-flash-image-preview  text input $0.50/1M, image output $60/1M
--                                   ($0.067 per 1K image)
-- gemini-3-pro-image (priced per-image, not cleanly per-token) is left
-- operator-configurable. Pairs with the streaming-image-preservation fix
-- (bb9d4d24). Unlisted models still FAIL CLOSED (0068 invariant).
BEGIN;
UPDATE billing_pricing_versions
SET pricing_data = jsonb_set(
        pricing_data,
        '{models}',
        COALESCE(pricing_data->'models', '{}'::jsonb) || $models${"gemini-2.5-flash-image":{"input_micro_usd":0.30,"output_micro_usd":30},"gemini-3.1-flash-image-preview":{"input_micro_usd":0.50,"output_micro_usd":60}}$models$::jsonb
    )
WHERE tenant_id = 0 AND version = '1.0';
COMMIT;
