ALTER TABLE provider_accounts
    ADD COLUMN IF NOT EXISTS last_request_observed_at timestamptz;

-- 历史被动请求观测不会写 latency。带 latency 的旧记录保留为潜在主动探测证据。
UPDATE provider_accounts
SET last_request_observed_at = last_probe_at
WHERE last_request_observed_at IS NULL
  AND last_probe_at IS NOT NULL
  AND last_probe_latency_ms IS NULL;

UPDATE provider_accounts
SET last_probe_at = NULL
WHERE last_probe_at IS NOT NULL
  AND last_probe_latency_ms IS NULL
  AND last_request_observed_at = last_probe_at;

COMMENT ON COLUMN provider_accounts.last_request_observed_at IS
    '最近一次普通请求完成事件的观测时间；不代表主动上游探测';
