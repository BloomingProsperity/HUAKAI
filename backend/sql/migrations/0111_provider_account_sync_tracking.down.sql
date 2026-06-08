ALTER TABLE provider_accounts
    DROP COLUMN IF EXISTS model_update_removed,
    DROP COLUMN IF EXISTS model_update_ignored,
    DROP COLUMN IF EXISTS model_update_detected,
    DROP COLUMN IF EXISTS model_sync_last_check_at;
