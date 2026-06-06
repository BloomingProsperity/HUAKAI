-- HUAKAI subscription admin lifecycle operations.
-- Adds only the DB surface required by admin plan update / extend / reset / revoke.

BEGIN;

ALTER TABLE user_subscriptions DROP CONSTRAINT IF EXISTS user_subscriptions_status_check;
ALTER TABLE user_subscriptions ADD CONSTRAINT user_subscriptions_status_check
    CHECK (status IN ('active', 'expired', 'cancelled', 'revoked'));

ALTER TABLE subscription_audit_events DROP CONSTRAINT IF EXISTS subscription_audit_events_event_type_check;
ALTER TABLE subscription_audit_events ADD CONSTRAINT subscription_audit_events_event_type_check
    CHECK (event_type IN (
        'subscription_created', 'subscription_renewed',
        'subscription_extended', 'subscription_quota_reset', 'subscription_revoked',
        'expired', 'cancelled', 'group_upgraded', 'group_downgraded', 'idempotent_replay'
    ));

CREATE TABLE IF NOT EXISTS subscription_plan_audit_events (
    id                bigserial   PRIMARY KEY,
    tenant_id         bigint      NOT NULL REFERENCES tenants(id),
    plan_id           bigint      NOT NULL,
    event_type        text        NOT NULL CHECK (event_type IN ('subscription_plan_updated')),
    actor_kind        text        NOT NULL DEFAULT 'system'
        CHECK (actor_kind IN ('admin', 'user', 'system')),
    actor_id          bigint,
    request_id        text,
    redacted_payload  jsonb,
    occurred_at       timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, plan_id) REFERENCES subscription_plans (tenant_id, id)
);

CREATE INDEX IF NOT EXISTS idx_subscription_plan_audit_events_plan
    ON subscription_plan_audit_events (tenant_id, plan_id, occurred_at, id);

COMMIT;
