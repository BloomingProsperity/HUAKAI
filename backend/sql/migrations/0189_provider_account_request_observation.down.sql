UPDATE provider_accounts
SET last_probe_at = last_request_observed_at
WHERE last_probe_at IS NULL
  AND last_request_observed_at IS NOT NULL;

ALTER TABLE provider_accounts
    DROP COLUMN IF EXISTS last_request_observed_at;
