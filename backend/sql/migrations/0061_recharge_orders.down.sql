-- 0061_recharge_orders.down.sql
--
-- DEV ROLLBACK ONLY. Drops the additive MONEY-3 recharge order table.

BEGIN;

DROP TABLE IF EXISTS recharge_orders;

COMMIT;
