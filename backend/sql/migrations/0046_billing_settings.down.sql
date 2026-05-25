-- 0046_billing_settings.down.sql
--
-- 回滚 Case C 计费策略的租户级设置表。

BEGIN;

DROP TABLE IF EXISTS billing_settings;

COMMIT;
