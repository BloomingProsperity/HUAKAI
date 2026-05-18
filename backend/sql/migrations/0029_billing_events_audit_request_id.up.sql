BEGIN;

ALTER TABLE billing_events
    ADD COLUMN IF NOT EXISTS audit_request_id TEXT;

CREATE INDEX IF NOT EXISTS idx_billing_events_tenant_audit_request
    ON billing_events(tenant_id, audit_request_id, occurred_at DESC)
    WHERE audit_request_id IS NOT NULL;

COMMENT ON COLUMN billing_events.audit_request_id IS
    'Gateway/audit ledger request_id copied from the HTTP request context so user receipts can join audit_ledger_entries to billing facts without conflating idempotency keys.';

COMMIT;
