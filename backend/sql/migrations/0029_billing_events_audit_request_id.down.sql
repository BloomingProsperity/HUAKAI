BEGIN;

DROP INDEX IF EXISTS idx_billing_events_tenant_audit_request;

ALTER TABLE billing_events
    DROP COLUMN IF EXISTS audit_request_id;

COMMIT;
