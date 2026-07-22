BEGIN;

-- 0217 已把 Hermes 模型来源收敛为外部 OpenAI 兼容配置，但遗漏了建表时生成的
-- api_source 列级检查约束，导致新旧约束互相冲突，任何设置都无法启用。
ALTER TABLE hermes_settings
    DROP CONSTRAINT hermes_settings_api_source_check,
    ADD CONSTRAINT hermes_settings_api_source_check
        CHECK (api_source = 'external_openai_compatible');

COMMIT;
