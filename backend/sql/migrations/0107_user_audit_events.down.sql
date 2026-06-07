-- DEV ROLLBACK ONLY — production rollback must export user_audit_events
-- before dropping because these rows are user-visible key management history.

BEGIN;

DROP TABLE IF EXISTS user_audit_events;

COMMIT;
