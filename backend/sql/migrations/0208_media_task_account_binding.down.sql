DROP INDEX IF EXISTS idx_media_tasks_provider_account_active;
DROP INDEX IF EXISTS idx_media_tasks_api_owner_request;

ALTER TABLE media_tasks
    DROP COLUMN IF EXISTS route_id,
    DROP COLUMN IF EXISTS provider_model_id,
    DROP COLUMN IF EXISTS requested_model,
    DROP COLUMN IF EXISTS protocol_family,
    DROP COLUMN IF EXISTS pool_group_id,
    DROP COLUMN IF EXISTS provider_account_id,
    DROP COLUMN IF EXISTS api_key_id;
