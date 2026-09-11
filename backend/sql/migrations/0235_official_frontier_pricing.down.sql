-- 0235 down:撤掉本迁移新种的模型键,并把 GPT-5.6 族恢复到 0173/0220 旧官方价。
-- 不碰运营自定义版本,不改 is_public。

BEGIN;

UPDATE billing_pricing_versions
SET pricing_data = jsonb_set(
        pricing_data,
        '{models}',
        (
            (COALESCE(pricing_data->'models', '{}'::jsonb)
                - 'gpt-6-astra'
                - 'claude-fable-5-1'
                - 'claude-opus-5'
                - 'gemini-3.6-flash'
                - 'gemini-3.7-flash'
                - 'gemini-3.8-flash'
                - 'grok-4.5'
                - 'grok-4.6')
            || $restore${
              "gpt-5.6":{"input_micro_usd":"5","output_micro_usd":"30","cache_read_micro_usd":"0.5"},
              "gpt-5.6-sol":{"input_micro_usd":"5","output_micro_usd":"30","cache_read_micro_usd":"0.5"},
              "gpt-5.6-terra":{"input_micro_usd":"2.5","output_micro_usd":"15","cache_read_micro_usd":"0.25"},
              "gpt-5.6-luna":{"input_micro_usd":"1","output_micro_usd":"6","cache_read_micro_usd":"0.1"}
            }$restore$::jsonb
        ),
        true)
WHERE tenant_id = 0 AND version = '1.0';

UPDATE billing_pricing_versions
SET pricing_data = jsonb_set(
        pricing_data,
        '{providers,openai,models}',
        (
            (COALESCE(pricing_data#>'{providers,openai,models}', '{}'::jsonb) - 'gpt-6-astra' - 'gpt-5.6')
            || $openai${
              "gpt-5.6-sol":{"input_micro_usd":"5","output_micro_usd":"30","cache_read_micro_usd":"0.5"},
              "gpt-5.6-terra":{"input_micro_usd":"2.5","output_micro_usd":"15","cache_read_micro_usd":"0.25"},
              "gpt-5.6-luna":{"input_micro_usd":"1","output_micro_usd":"6","cache_read_micro_usd":"0.1"}
            }$openai$::jsonb
        ),
        true)
WHERE tenant_id = 0 AND version = '1.0'
  AND pricing_data#>'{providers,openai}' IS NOT NULL;

COMMIT;
