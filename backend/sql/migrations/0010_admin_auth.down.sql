-- Down migration for 0010.
-- DEV ROLLBACK ONLY — production rollback must export admin_audit_events
-- before dropping (those rows are operator-action audit trail).

BEGIN;

DROP TABLE IF EXISTS admin_audit_events;
DROP TABLE IF EXISTS admin_tokens;

COMMIT;
