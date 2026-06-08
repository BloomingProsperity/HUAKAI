ALTER TABLE provider_accounts ADD COLUMN IF NOT EXISTS max_sessions integer NOT NULL DEFAULT 0;
