BEGIN;

DROP INDEX IF EXISTS idx_cost_disputes_user_created;
DROP INDEX IF EXISTS idx_cost_disputes_tenant_status_created;
DROP TABLE IF EXISTS cost_disputes;

COMMIT;

