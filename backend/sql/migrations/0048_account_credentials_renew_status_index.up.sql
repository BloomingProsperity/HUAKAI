-- 0048_account_credentials_renew_status_index.up.sql
--
-- 支撑 /renew 跨租户续期状态页按 updated_at DESC, id DESC 做 keyset 分页。
-- 只给未删除凭据建普通索引；迁移框架在事务中执行，不使用 CONCURRENTLY。

BEGIN;

CREATE INDEX IF NOT EXISTS idx_account_credentials_renew_status
    ON account_credentials (updated_at DESC, id DESC)
    WHERE deleted_at IS NULL;

COMMIT;
