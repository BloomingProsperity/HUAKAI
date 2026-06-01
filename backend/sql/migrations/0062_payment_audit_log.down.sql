-- 0062_payment_audit_log.down.sql
--
-- DEV ROLLBACK ONLY. Drops the additive MONEY-4 payment callback audit log.

BEGIN;

DROP TABLE IF EXISTS payment_audit_log;

COMMIT;
