-- 0015_obs_dlq_extend.up.sql
--
-- F-OBS-005: usage_record_dlq 从单一 usage fallback 扩展为通用
-- observability DLQ。Owner OCAW-16/17/18/19/20/21 已批准：
-- generic event kinds + priority lane + lease + replica + idempotency。
--
-- Dry-run 思路:
-- 1. 仅做 additive columns / indexes / CHECK 约束，保留旧 usage_record_dlq。
-- 2. 旧行用 legacy:<id> 回填 idempotency_key，避免唯一索引拒绝。
-- 3. claim_id 放宽为 nullable，用于 account_health/metrics 等非 claim 事件。
-- 4. down migration 在发现 claim_id IS NULL 时保守失败，避免静默丢 generic 行。

BEGIN;

ALTER TABLE usage_record_dlq
    ADD COLUMN IF NOT EXISTS event_kind text,
    ADD COLUMN IF NOT EXISTS lane text,
    ADD COLUMN IF NOT EXISTS status text,
    ADD COLUMN IF NOT EXISTS next_retry_at timestamptz,
    ADD COLUMN IF NOT EXISTS lease_ttl interval,
    ADD COLUMN IF NOT EXISTS lease_owner text,
    ADD COLUMN IF NOT EXISTS lease_until timestamptz,
    ADD COLUMN IF NOT EXISTS replica_status text,
    ADD COLUMN IF NOT EXISTS replica_target text,
    ADD COLUMN IF NOT EXISTS replica_committed_at timestamptz,
    ADD COLUMN IF NOT EXISTS idempotency_key text,
    ADD COLUMN IF NOT EXISTS source_table text,
    ADD COLUMN IF NOT EXISTS source_id bigint,
    ADD COLUMN IF NOT EXISTS operator_review_at timestamptz,
    ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now();

UPDATE usage_record_dlq
SET event_kind = COALESCE(event_kind, 'usage_record'),
    lane = COALESCE(lane, 'HIGH'),
    status = COALESCE(status, CASE WHEN replayed_at IS NULL THEN 'pending' ELSE 'delivered' END),
    next_retry_at = COALESCE(next_retry_at, failure_at),
    lease_ttl = COALESCE(lease_ttl, interval '30 seconds'),
    replica_status = COALESCE(replica_status, 'none'),
    replica_target = COALESCE(replica_target, 'primary'),
    idempotency_key = COALESCE(idempotency_key, 'legacy:' || id::text),
    source_table = COALESCE(source_table, 'usage_records'),
    source_id = COALESCE(source_id, claim_id),
    updated_at = COALESCE(updated_at, now());

ALTER TABLE usage_record_dlq
    ALTER COLUMN event_kind SET NOT NULL,
    ALTER COLUMN event_kind SET DEFAULT 'usage_record',
    ALTER COLUMN lane SET NOT NULL,
    ALTER COLUMN lane SET DEFAULT 'HIGH',
    ALTER COLUMN status SET NOT NULL,
    ALTER COLUMN status SET DEFAULT 'pending',
    ALTER COLUMN next_retry_at SET NOT NULL,
    ALTER COLUMN next_retry_at SET DEFAULT now(),
    ALTER COLUMN lease_ttl SET NOT NULL,
    ALTER COLUMN lease_ttl SET DEFAULT interval '30 seconds',
    ALTER COLUMN replica_status SET NOT NULL,
    ALTER COLUMN replica_status SET DEFAULT 'none',
    ALTER COLUMN replica_target SET NOT NULL,
    ALTER COLUMN replica_target SET DEFAULT 'primary',
    ALTER COLUMN idempotency_key SET NOT NULL,
    ALTER COLUMN source_table SET NOT NULL,
    ALTER COLUMN source_table SET DEFAULT 'usage_records',
    ALTER COLUMN claim_id DROP NOT NULL;

ALTER TABLE usage_record_dlq
    DROP CONSTRAINT IF EXISTS usage_record_dlq_event_kind_check,
    ADD CONSTRAINT usage_record_dlq_event_kind_check
        CHECK (event_kind IN
            ('usage_record', 'billing_event_replica', 'audit_event_replica',
             'account_health', 'metrics')),
    DROP CONSTRAINT IF EXISTS usage_record_dlq_lane_check,
    ADD CONSTRAINT usage_record_dlq_lane_check
        CHECK (lane IN ('HIGH', 'MED', 'LOW')),
    DROP CONSTRAINT IF EXISTS usage_record_dlq_status_check,
    ADD CONSTRAINT usage_record_dlq_status_check
        CHECK (status IN
            ('pending', 'inflight', 'delivered', 'operator_review', 'dlq',
             'quarantined')),
    DROP CONSTRAINT IF EXISTS usage_record_dlq_replica_status_check,
    ADD CONSTRAINT usage_record_dlq_replica_status_check
        CHECK (replica_status IN ('none', 'pending', 'delivered', 'failed'));

CREATE UNIQUE INDEX IF NOT EXISTS uq_usage_dlq_idempotency
    ON usage_record_dlq (tenant_id, event_kind, idempotency_key, replica_target);

CREATE INDEX IF NOT EXISTS idx_usage_dlq_claim_due
    ON usage_record_dlq (lane, next_retry_at, id)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_usage_dlq_lease_expired
    ON usage_record_dlq (lane, lease_until, id)
    WHERE status = 'inflight';

CREATE INDEX IF NOT EXISTS idx_usage_dlq_operator_review
    ON usage_record_dlq (tenant_id, event_kind, failure_at DESC)
    WHERE status IN ('operator_review', 'dlq', 'quarantined');

CREATE INDEX IF NOT EXISTS idx_usage_dlq_replica_status
    ON usage_record_dlq (replica_status, replica_target, failure_at DESC)
    WHERE replica_status IN ('pending', 'failed');

COMMENT ON TABLE usage_record_dlq IS
    'F-OBS-005: generic observability DLQ with priority lane, lease, replica, idempotency, and platform-admin replay.';

COMMENT ON COLUMN usage_record_dlq.lane IS
    'F-OBS-005 priority lane: HIGH=Billing/Audit, MED=AccountHealth, LOW=Metrics drain-on-shutdown.';
COMMENT ON COLUMN usage_record_dlq.idempotency_key IS
    'Stable per-event key used by retry/replay/replica to avoid duplicate money-path effects.';
COMMENT ON COLUMN usage_record_dlq.replica_target IS
    'Replica sink target label. v1 default is separate PostgreSQL DSN when configured.';

COMMIT;
