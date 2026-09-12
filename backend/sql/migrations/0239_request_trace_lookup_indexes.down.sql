BEGIN;

DROP INDEX IF EXISTS idx_oauth_refresh_audit_request;
DROP INDEX IF EXISTS idx_rate_limit_audit_upstream_request;
DROP INDEX IF EXISTS idx_pool_routing_audit_request;
DROP INDEX IF EXISTS idx_channel_health_audit_request;
DROP INDEX IF EXISTS idx_billing_events_audit_request;
DROP INDEX IF EXISTS idx_billing_ledger_claims_logical_request;

COMMIT;
