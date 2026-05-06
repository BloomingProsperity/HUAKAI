-- 0012_provider_accounts_proxy_url.down.sql
--
-- 回滚 0012_up：删除 provider_accounts.proxy_url 列。
--
-- 注意：执行此 down 会**丢失所有 proxy_url 配置数据**。如有需要在 down
-- 之前先 dump 出来。

BEGIN;

ALTER TABLE provider_accounts DROP COLUMN IF EXISTS proxy_url;

COMMIT;
