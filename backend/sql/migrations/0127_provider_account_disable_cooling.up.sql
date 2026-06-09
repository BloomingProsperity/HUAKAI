ALTER TABLE provider_accounts ADD COLUMN IF NOT EXISTS disable_cooling boolean NOT NULL DEFAULT false;
