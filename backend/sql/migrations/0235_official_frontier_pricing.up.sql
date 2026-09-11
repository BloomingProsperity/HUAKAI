-- 0235:把 2026-07-23 价目种子之后官方已发布的前沿文本模型补进默认公开定价
-- (tenant 0, version '1.0')。单位 = micro-USD/token(= USD / 1M tokens)。
--
-- 核实日期 2026-09-10,来源为各厂商现行公开价目:
--   OpenAI GPT-6 Astra          $10 / $50, cache-read $1, cache-write $12.50
--   OpenAI GPT-5.6 Sol 现行价   $4 / $20 (2026-08-21 起至少到 2026-11-21)
--   OpenAI GPT-5.6 Terra        $2 / $12 (2026-07-30)
--   OpenAI GPT-5.6 Luna         $0.20 / $1.20 (2026-07-30)
--   Claude Fable 5.1            $10 / $50, cache-read $0.25, 5m write $12.50, 1h write $20
--   Claude Opus 5               $5 / $25, cache-read $0.5, cache-write $6.25
--   Gemini 3.6/3.7/3.8 Flash    引入价 $0.75 / $3.75 (至 2026-12-31)
--   Grok 4.5 / 4.6 (<200k)      $2 / $6; 4.5 cache-read $0.30, 4.6 cache-read $0.50
--
-- gpt-5.6 别名跟随 Sol 现行价。长上下文加价(Astra >272k、Grok >=200k)仍走
-- 后续阶梯合同,本迁移只种标准档,避免新模型因缺价 FAIL CLOSED 成 503。
--
-- 查找顺序是 providers.<vendor>.models 优先于 models。0173 已把 Sol/Terra/Luna
-- 写进 providers.openai.models,因此这两处必须一起改,否则热路径仍吃旧价。
-- 只改 tenant 0 / version 1.0,不动 is_public,不覆盖其它租户自定义版本。

BEGIN;

WITH frontier_models AS (
    SELECT $models${
      "gpt-5.6":{"input_micro_usd":"4","output_micro_usd":"20","cache_read_micro_usd":"0.4"},
      "gpt-5.6-sol":{"input_micro_usd":"4","output_micro_usd":"20","cache_read_micro_usd":"0.4"},
      "gpt-5.6-terra":{"input_micro_usd":"2","output_micro_usd":"12","cache_read_micro_usd":"0.2"},
      "gpt-5.6-luna":{"input_micro_usd":"0.2","output_micro_usd":"1.2","cache_read_micro_usd":"0.02"},
      "gpt-6-astra":{"input_micro_usd":"10","output_micro_usd":"50","cache_read_micro_usd":"1","cache_creation_micro_usd":"12.5"},
      "claude-fable-5-1":{"input_micro_usd":"10","output_micro_usd":"50","cache_read_micro_usd":"0.25","cache_creation_micro_usd":"12.5","cache_creation_1h_micro_usd":"20"},
      "claude-opus-5":{"input_micro_usd":"5","output_micro_usd":"25","cache_read_micro_usd":"0.5","cache_creation_micro_usd":"6.25"},
      "gemini-3.6-flash":{"input_micro_usd":"0.75","output_micro_usd":"3.75","cache_read_micro_usd":"0.075"},
      "gemini-3.7-flash":{"input_micro_usd":"0.75","output_micro_usd":"3.75","cache_read_micro_usd":"0.075"},
      "gemini-3.8-flash":{"input_micro_usd":"0.75","output_micro_usd":"3.75","cache_read_micro_usd":"0.075"},
      "grok-4.5":{"input_micro_usd":"2","output_micro_usd":"6","cache_read_micro_usd":"0.3"},
      "grok-4.6":{"input_micro_usd":"2","output_micro_usd":"6","cache_read_micro_usd":"0.5"}
    }$models$::jsonb AS value
)
UPDATE billing_pricing_versions
SET pricing_data = jsonb_set(
        COALESCE(pricing_data, '{}'::jsonb),
        '{models}',
        COALESCE(pricing_data->'models', '{}'::jsonb) || (SELECT value FROM frontier_models),
        true)
WHERE tenant_id = 0 AND version = '1.0';

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
        COALESCE(pricing_data#>'{providers,openai,models}', '{}'::jsonb) || $openai${
          "gpt-5.6":{"input_micro_usd":"4","output_micro_usd":"20","cache_read_micro_usd":"0.4"},
          "gpt-5.6-sol":{"input_micro_usd":"4","output_micro_usd":"20","cache_read_micro_usd":"0.4"},
          "gpt-5.6-terra":{"input_micro_usd":"2","output_micro_usd":"12","cache_read_micro_usd":"0.2"},
          "gpt-5.6-luna":{"input_micro_usd":"0.2","output_micro_usd":"1.2","cache_read_micro_usd":"0.02"},
          "gpt-6-astra":{"input_micro_usd":"10","output_micro_usd":"50","cache_read_micro_usd":"1","cache_creation_micro_usd":"12.5"}
        }$openai$::jsonb,
        true)
WHERE tenant_id = 0 AND version = '1.0';

COMMIT;
