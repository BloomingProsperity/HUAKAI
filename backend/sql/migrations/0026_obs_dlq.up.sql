-- F-OBS-005 generic async outbox + DLQ.
-- Additive migration: does not modify legacy usage_record_dlq/admin replay paths.

BEGIN;

CREATE TABLE IF NOT EXISTS outbox_events (
    id text PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    event_type text NOT NULL,
    priority text NOT NULL CHECK (priority IN ('default', 'high', 'critical')),
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_retry_at timestamptz NOT NULL DEFAULT now(),
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'processing', 'completed', 'failed_retry', 'failed_dead')),
    failure_reason text
);

CREATE INDEX IF NOT EXISTS idx_outbox_events_due_priority
    ON outbox_events (
        status,
        (CASE priority WHEN 'critical' THEN 3 WHEN 'high' THEN 2 ELSE 1 END) DESC,
        next_retry_at,
        created_at,
        id
    )
    WHERE status IN ('pending', 'failed_retry', 'processing');

CREATE INDEX IF NOT EXISTS idx_outbox_events_tenant_status
    ON outbox_events (tenant_id, status, created_at DESC);

CREATE TABLE IF NOT EXISTS dlq_events (
    id text PRIMARY KEY,
    outbox_event_id text NOT NULL REFERENCES outbox_events(id) ON DELETE CASCADE,
    tenant_id bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    dead_at timestamptz NOT NULL DEFAULT now(),
    dead_reason text NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_dlq_events_tenant_dead_at
    ON dlq_events (tenant_id, dead_at DESC);

COMMENT ON TABLE outbox_events IS
    'F-OBS-005 generic async outbox for email, audit refund, channel alert, and other redacted tenant events.';
COMMENT ON COLUMN outbox_events.payload IS
    'Redacted JSON payload only; raw prompt/token/cookie/credential data must not be stored here.';
COMMENT ON COLUMN outbox_events.failure_reason IS
    'Redacted failure reason only; no raw upstream body, token, cookie, prompt, or credential.';
COMMENT ON TABLE dlq_events IS
    'F-OBS-005 durable DLQ rows for events exhausted by retry policy.';
COMMENT ON COLUMN dlq_events.dead_reason IS
    'Redacted dead-letter reason only.';

COMMIT;
