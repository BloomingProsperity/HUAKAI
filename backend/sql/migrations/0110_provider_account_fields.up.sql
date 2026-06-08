ALTER TABLE provider_accounts
    ADD COLUMN IF NOT EXISTS static_weight integer NOT NULL DEFAULT 1 CHECK (static_weight >= 1),
    ADD COLUMN IF NOT EXISTS probe_model text,
    ADD COLUMN IF NOT EXISTS tags text[] NOT NULL DEFAULT ARRAY[]::text[],
    ADD COLUMN IF NOT EXISTS extra jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS last_probe_latency_ms integer,
    ADD COLUMN IF NOT EXISTS last_probe_at timestamptz;

CREATE INDEX IF NOT EXISTS idx_provider_accounts_tags
    ON provider_accounts USING gin (tags);
