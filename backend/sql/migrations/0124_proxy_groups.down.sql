BEGIN;
DROP INDEX IF EXISTS idx_proxies_group;
ALTER TABLE provider_accounts DROP COLUMN IF EXISTS proxy_group_id;
ALTER TABLE proxies DROP COLUMN IF EXISTS group_id;
COMMIT;
