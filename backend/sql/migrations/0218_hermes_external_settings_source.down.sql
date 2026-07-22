BEGIN;

ALTER TABLE hermes_settings
    DROP CONSTRAINT hermes_settings_api_source_check,
    DROP CONSTRAINT hermes_settings_source_profile_consistency;

-- 回到旧来源合同前先停用并解除外部配置引用，避免现有行让约束恢复失败。
UPDATE hermes_settings
SET enabled = FALSE,
    api_source = 'managed_huakai_api',
    profile_id = NULL,
    updated_at = clock_timestamp();

ALTER TABLE hermes_settings
    ADD CONSTRAINT hermes_settings_api_source_check
        CHECK (api_source IN ('managed_huakai_api', 'dedicated_group')),
    ADD CONSTRAINT hermes_settings_source_profile_consistency CHECK (
        (api_source = 'managed_huakai_api' AND profile_id IS NULL)
        OR
        (api_source = 'dedicated_group' AND profile_id IS NOT NULL)
    );

COMMIT;
