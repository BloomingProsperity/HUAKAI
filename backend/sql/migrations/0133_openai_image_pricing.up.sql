-- 0133: seed official OpenAI DALL-E image-generation prices into the default
-- pricing version (tenant 0, '1.0'), under providers.openai.models so the
-- per-image catalog (internal/imagepricing) resolves them. Without this, image
-- generation fails closed (pricing_unavailable -> 503) for every OpenAI image
-- request. Each image_base_micro_usd is USD/image x 1e6; cost = n x base x
-- size_multiplier x quality_multiplier / 1e6. Verified vs OpenAI official docs:
--   dall-e-3 standard 1024x1024 $0.04, 1024x1792 / 1792x1024 $0.08;
--            hd       1024x1024 $0.08, 1024x1792 / 1792x1024 $0.12.
--   dall-e-2 256x256 $0.016, 512x512 $0.018, 1024x1024 $0.020.
-- gpt-image-1 (token-with-quality scheme) and Gemini Imagen are intentionally
-- left operator-configurable (admin can set prices); unlisted models FAIL CLOSED.
-- Text pricing under the top-level "models" map is untouched (image lookups read
-- providers.openai.models first, text falls through to "models").
BEGIN;
UPDATE billing_pricing_versions
SET pricing_data = jsonb_set(
        jsonb_set(
            jsonb_set(
                pricing_data,
                '{providers}',
                COALESCE(pricing_data->'providers', '{}'::jsonb),
                true),
            '{providers,openai}',
            COALESCE(pricing_data#>'{providers,openai}', '{}'::jsonb),
            true),
        '{providers,openai,models}',
        COALESCE(pricing_data#>'{providers,openai,models}', '{}'::jsonb) || $img${"dall-e-3":{"pricing_scheme":"per_image","image_base_micro_usd":"40000","image_size_multipliers":{"1024x1024":"1","1024x1792":"2","1792x1024":"2"},"image_quality_multipliers":{"standard":"1","hd":"2","hd@1024x1792":"1.5","hd@1792x1024":"1.5"},"image_amount_range":{"min":1,"max":1},"image_prompt_max_chars":4000},"dall-e-2":{"pricing_scheme":"per_image","image_base_micro_usd":"16000","image_size_multipliers":{"256x256":"1","512x512":"1.125","1024x1024":"1.25"},"image_quality_multipliers":{"standard":"1"},"image_amount_range":{"min":1,"max":10},"image_prompt_max_chars":1000}}$img$::jsonb,
        true)
WHERE tenant_id = 0 AND version = '1.0';
COMMIT;
