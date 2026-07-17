-- 回滚 0186:从全局默认定价版本移除本迁移补录的 5 个外国文本模型价。
BEGIN;

UPDATE billing_pricing_versions
SET pricing_data = jsonb_set(
        pricing_data,
        '{models}',
        (pricing_data->'models')
            - 'gpt-4o-mini'
            - 'claude-3-5-haiku-latest'
            - 'grok-3-mini'
            - 'gemini-2.5-flash-lite'
            - 'moonshot-v1-8k'
    )
WHERE tenant_id = 0 AND version = '1.0';

COMMIT;
