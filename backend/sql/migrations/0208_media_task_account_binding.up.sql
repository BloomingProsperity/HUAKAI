-- 异步媒体任务必须固定创建时使用的客户 Key、账号池和上游账号。
-- 历史任务允许为空；新账号转 API 入口由应用层强制写全。
ALTER TABLE media_tasks
    ADD COLUMN IF NOT EXISTS api_key_id bigint REFERENCES api_keys(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS provider_account_id bigint REFERENCES provider_accounts(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS pool_group_id bigint REFERENCES pool_groups(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS protocol_family text,
    ADD COLUMN IF NOT EXISTS requested_model text,
    ADD COLUMN IF NOT EXISTS provider_model_id text,
    ADD COLUMN IF NOT EXISTS route_id text;

CREATE INDEX IF NOT EXISTS idx_media_tasks_api_owner_request
    ON media_tasks (tenant_id, user_id, api_key_id, request_id);

CREATE INDEX IF NOT EXISTS idx_media_tasks_provider_account_active
    ON media_tasks (tenant_id, provider_account_id, status)
    WHERE provider_account_id IS NOT NULL
      AND status IN ('queued', 'in_progress');
