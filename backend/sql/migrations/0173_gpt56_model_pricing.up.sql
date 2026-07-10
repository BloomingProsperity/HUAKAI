-- 0173:把官方 OpenAI GPT-5.6(Sol/Terra/Luna)文本 token 定价 seed 进默认定价版本
-- (tenant 0,'1.0')的 providers.openai.models。官方差异化每 1M 费率(micro-USD/token = $/1M):
--   gpt-5.6-sol   input $5   / output $30 / cached-input $0.5
--   gpt-5.6-terra input $2.5 / output $15 / cached-input $0.25
--   gpt-5.6-luna  input $1   / output $6  / cached-input $0.1
-- cached-input 取 input/10(OpenAI 缓存输入折扣惯例)。文本 token 方案(不写 pricing_scheme
-- = 默认 token 计费)。未列模型仍 FAIL CLOSED(0068 兜底 → 503)。effort max 上游放行、
-- 经 HUAKAI 字节直通;ultra 为幻影档、上游拒。gpt-5.6 有最低 Codex 版本门(账号不 pin
-- codex_version 则无 Version 头、天然可用)。
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
        COALESCE(pricing_data#>'{providers,openai,models}', '{}'::jsonb) || $p${"gpt-5.6-sol":{"input_micro_usd":"5","output_micro_usd":"30","cache_read_micro_usd":"0.5"},"gpt-5.6-terra":{"input_micro_usd":"2.5","output_micro_usd":"15","cache_read_micro_usd":"0.25"},"gpt-5.6-luna":{"input_micro_usd":"1","output_micro_usd":"6","cache_read_micro_usd":"0.1"}}$p$::jsonb,
        true)
WHERE tenant_id = 0 AND version = '1.0';
COMMIT;
