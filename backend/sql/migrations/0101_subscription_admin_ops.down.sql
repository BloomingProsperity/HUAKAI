-- Roll back subscription admin lifecycle operation constraints.
-- Fail closed when rows already depend on the new admin semantics.

BEGIN;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM user_subscriptions WHERE status = 'revoked' LIMIT 1) THEN
        RAISE EXCEPTION 'refuse rollback 0101: revoked subscription rows exist'
            USING ERRCODE = 'check_violation';
    END IF;
    IF EXISTS (
        SELECT 1 FROM subscription_audit_events
        WHERE event_type IN ('subscription_extended', 'subscription_quota_reset', 'subscription_revoked')
        LIMIT 1
    ) THEN
        RAISE EXCEPTION 'refuse rollback 0101: admin subscription audit events exist'
            USING ERRCODE = 'check_violation';
    END IF;
    IF EXISTS (SELECT 1 FROM subscription_plan_audit_events LIMIT 1) THEN
        RAISE EXCEPTION 'refuse rollback 0101: subscription plan audit events exist'
            USING ERRCODE = 'check_violation';
    END IF;
END $$;

DROP TABLE IF EXISTS subscription_plan_audit_events;

ALTER TABLE subscription_audit_events DROP CONSTRAINT IF EXISTS subscription_audit_events_event_type_check;
ALTER TABLE subscription_audit_events ADD CONSTRAINT subscription_audit_events_event_type_check
    CHECK (event_type IN (
        'subscription_created', 'subscription_renewed', 'expired', 'cancelled',
        'group_upgraded', 'group_downgraded', 'idempotent_replay'
    ));

ALTER TABLE user_subscriptions DROP CONSTRAINT IF EXISTS user_subscriptions_status_check;
ALTER TABLE user_subscriptions ADD CONSTRAINT user_subscriptions_status_check
    CHECK (status IN ('active', 'expired', 'cancelled'));

COMMIT;
