ALTER TABLE provider_accounts ADD COLUMN IF NOT EXISTS window_cost_limit_cents bigint NOT NULL DEFAULT 0;
