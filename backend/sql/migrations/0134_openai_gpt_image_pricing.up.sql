-- 0134: seed official OpenAI gpt-image token prices into the default pricing
-- version (tenant 0, '1.0'), under providers.openai.models. token_image scheme:
-- the FINAL bill uses the upstream-reported usage (input_tokens x input rate +
-- output_tokens x output rate); image_output_token_upper_bound only sizes the
-- reservation hold (settle uses actual tokens). For image *generation* the input
-- is text only, so input_micro_usd is the text-input rate. Verified vs OpenAI:
--   gpt-image-1   text input $5/1M, image output $40/1M
--                 (~$0.02/$0.07/$0.19 per low/medium/high 1024^2 image).
--   gpt-image-1.5 text input $5/1M, image output $32/1M.
-- (image-input tokens for edits bill at the text rate -- a mild under-charge, never
-- over-charge.) gpt-image-1-mini / gpt-image-2 left operator-configurable. Unlisted
-- models still FAIL CLOSED (0068). Pairs with the DALL-E per-image seed (0133).
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
        COALESCE(pricing_data#>'{providers,openai,models}', '{}'::jsonb) || $img${"gpt-image-1":{"pricing_scheme":"token_image","input_micro_usd":"5","output_micro_usd":"40","image_output_token_upper_bound":{"1024x1024":4160,"1024x1536":6240,"1536x1024":6208,"auto":6240},"image_size_multipliers":{"1024x1024":"1","1024x1536":"1","1536x1024":"1","auto":"1"},"image_amount_range":{"min":1,"max":10},"image_prompt_max_chars":32000},"gpt-image-1.5":{"pricing_scheme":"token_image","input_micro_usd":"5","output_micro_usd":"32","image_output_token_upper_bound":{"1024x1024":4160,"1024x1536":6240,"1536x1024":6208,"auto":6240},"image_size_multipliers":{"1024x1024":"1","1024x1536":"1","1536x1024":"1","auto":"1"},"image_amount_range":{"min":1,"max":10},"image_prompt_max_chars":32000}}$img$::jsonb,
        true)
WHERE tenant_id = 0 AND version = '1.0';
COMMIT;
