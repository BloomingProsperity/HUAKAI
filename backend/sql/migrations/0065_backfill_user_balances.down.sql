-- 0065_backfill_user_balances.down.sql
--
-- No destructive rollback. Rows inserted or raised in user_balances become live
-- money state as soon as mandatory enforcement is active, and cannot be safely
-- distinguished from legitimate top-ups after this migration runs.

BEGIN;

COMMIT;
