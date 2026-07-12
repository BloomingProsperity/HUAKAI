-- 0179：补齐默认价表中 DeepSeek 缓存命中费率。单位沿用 0131：
-- micro-USD/token 等于 USD/百万 token；命中价取同版本 input 的 1/10。
BEGIN;
WITH cache_rates AS (
    SELECT $cache_rates${"deepseek-chat":{"cache_read_micro_usd":0.028},"deepseek-coder":{"cache_read_micro_usd":0.014},"deepseek-reasoner":{"cache_read_micro_usd":0.028}}$cache_rates$::jsonb AS value
)
UPDATE billing_pricing_versions
SET pricing_data = jsonb_set(
        jsonb_set(
            jsonb_set(
                pricing_data,
                '{models,deepseek-chat}',
                (pricing_data #> '{models,deepseek-chat}') || (cache_rates.value -> 'deepseek-chat'),
                false),
            '{models,deepseek-coder}',
            (pricing_data #> '{models,deepseek-coder}') || (cache_rates.value -> 'deepseek-coder'),
            false),
        '{models,deepseek-reasoner}',
        (pricing_data #> '{models,deepseek-reasoner}') || (cache_rates.value -> 'deepseek-reasoner'),
        false)
FROM cache_rates
WHERE tenant_id = 0
  AND version = '1.0'
  AND jsonb_typeof(pricing_data #> '{models,deepseek-chat}') = 'object'
  AND jsonb_typeof(pricing_data #> '{models,deepseek-coder}') = 'object'
  AND jsonb_typeof(pricing_data #> '{models,deepseek-reasoner}') = 'object';
COMMIT;
