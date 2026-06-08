DROP INDEX IF EXISTS idx_provider_accounts_tags;

ALTER TABLE provider_accounts
    DROP COLUMN IF EXISTS last_probe_at,
    DROP COLUMN IF EXISTS last_probe_latency_ms,
    DROP COLUMN IF EXISTS extra,
    DROP COLUMN IF EXISTS tags,
    DROP COLUMN IF EXISTS probe_model,
    DROP COLUMN IF EXISTS static_weight;
