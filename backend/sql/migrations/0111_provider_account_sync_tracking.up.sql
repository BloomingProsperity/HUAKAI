ALTER TABLE provider_accounts
    ADD COLUMN IF NOT EXISTS model_sync_last_check_at timestamptz,
    ADD COLUMN IF NOT EXISTS model_update_detected jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS model_update_ignored jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS model_update_removed jsonb NOT NULL DEFAULT '[]'::jsonb;
