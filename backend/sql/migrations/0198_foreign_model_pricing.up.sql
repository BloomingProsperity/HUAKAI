-- 0198: 补录外国文本模型官方价进全局默认定价版本(tenant 0, version '1.0', is_public)。
-- 与 0131 同一条路:官方价来源 = litellm 上游 model_prices_and_context_window.json(MIT 许可,
-- 公开权威价目聚合)。每值 = USD per 1M tokens(即 input_micro_usd 语义,与 0131 一致)。
-- 未列模型仍 FAIL CLOSED(0068 不变量),不改。
--
-- litellm 源键 → 本项目模型名(按请求实际携带的 model 名录入):
--   gpt-4o-mini(litellm: gpt-4o-mini)
--   claude-3-5-haiku-latest(litellm: claude-3-5-haiku-20241022,官方基座价一致)
--   grok-3-mini(litellm: xai/grok-3-mini)
--   gemini-2.5-flash-lite(litellm: gemini-2.5-flash-lite)
--   moonshot-v1-8k(litellm: moonshot/moonshot-v1-8k)
BEGIN;

UPDATE billing_pricing_versions
SET pricing_data = jsonb_set(
        pricing_data,
        '{models}',
        COALESCE(pricing_data->'models', '{}'::jsonb) || $models${
            "gpt-4o-mini": {"input_micro_usd": 0.15, "output_micro_usd": 0.60, "cache_read_micro_usd": 0.075},
            "claude-3-5-haiku-latest": {"input_micro_usd": 0.80, "output_micro_usd": 4.00, "cache_read_micro_usd": 0.08},
            "grok-3-mini": {"input_micro_usd": 0.30, "output_micro_usd": 0.50, "cache_read_micro_usd": 0.075},
            "gemini-2.5-flash-lite": {"input_micro_usd": 0.10, "output_micro_usd": 0.40, "cache_read_micro_usd": 0.01},
            "moonshot-v1-8k": {"input_micro_usd": 0.20, "output_micro_usd": 2.00}
        }$models$::jsonb
    )
WHERE tenant_id = 0 AND version = '1.0';

COMMIT;
