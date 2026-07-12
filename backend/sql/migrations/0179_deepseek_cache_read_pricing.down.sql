-- 回滚 0179：只移除本迁移新增的缓存命中费率，保留模型其它定价。
BEGIN;
UPDATE billing_pricing_versions
SET pricing_data = jsonb_set(
        jsonb_set(
            jsonb_set(
                pricing_data,
                '{models,deepseek-chat}',
                (pricing_data #> '{models,deepseek-chat}') - 'cache_read_micro_usd',
                false),
            '{models,deepseek-coder}',
            (pricing_data #> '{models,deepseek-coder}') - 'cache_read_micro_usd',
            false),
        '{models,deepseek-reasoner}',
        (pricing_data #> '{models,deepseek-reasoner}') - 'cache_read_micro_usd',
        false)
WHERE tenant_id = 0
  AND version = '1.0'
  AND jsonb_typeof(pricing_data #> '{models,deepseek-chat}') = 'object'
  AND jsonb_typeof(pricing_data #> '{models,deepseek-coder}') = 'object'
  AND jsonb_typeof(pricing_data #> '{models,deepseek-reasoner}') = 'object';
COMMIT;
