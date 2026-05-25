-- 0048_account_credentials_renew_status_index.down.sql
--
-- 回滚 /renew 跨租户续期状态分页索引。

BEGIN;

DROP INDEX IF EXISTS idx_account_credentials_renew_status;

COMMIT;
