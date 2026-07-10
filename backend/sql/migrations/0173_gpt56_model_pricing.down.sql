-- 回滚 0173:从默认定价版本移除 GPT-5.6 三档。外科式(只删 gpt-5.6-* 键),
-- 保留 providers.openai.models 下其它内容(如 0134 的 gpt-image、0133 的 DALL-E)。
BEGIN;
UPDATE billing_pricing_versions
SET pricing_data = jsonb_set(
        pricing_data,
        '{providers,openai,models}',
        (pricing_data#>'{providers,openai,models}') - 'gpt-5.6-sol' - 'gpt-5.6-terra' - 'gpt-5.6-luna')
WHERE tenant_id = 0 AND version = '1.0'
  AND pricing_data #> '{providers,openai,models}' IS NOT NULL;
COMMIT;
