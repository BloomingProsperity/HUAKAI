-- 0132: seed official MiniMax text-chat prices into the default pricing version
-- (tenant 0, '1.0'). Owner-directed (官方价默认); each value is USD per 1M tokens,
-- verified vs platform.minimax.io/docs/guides/pricing-paygo: M2 / M2.1 / M2.7 / M3
-- are all $0.30 input / $1.20 output at the standard tier. The M3 >512k long-context
-- tier ($0.60/$2.40) is intentionally not modeled by flat pricing -- a >512k request
-- under-charges slightly, never over-charges. Unlisted models still FAIL CLOSED
-- (0068 invariant). MiniMax registers as an OpenAI-compatible passthrough (681851e3).
BEGIN;
UPDATE billing_pricing_versions
SET pricing_data = jsonb_set(
        pricing_data,
        '{models}',
        COALESCE(pricing_data->'models', '{}'::jsonb) || $models${"MiniMax-M2":{"input_micro_usd":0.3,"output_micro_usd":1.2},"MiniMax-M2.1":{"input_micro_usd":0.3,"output_micro_usd":1.2},"MiniMax-M2.7":{"input_micro_usd":0.3,"output_micro_usd":1.2},"MiniMax-M3":{"input_micro_usd":0.3,"output_micro_usd":1.2}}$models$::jsonb
    )
WHERE tenant_id = 0 AND version = '1.0';
COMMIT;
