-- 0038_proxies_pool_and_account_links.down.sql
-- 反向: 删 trigger + 复 proxy_url 列 + 清 FK + 删 proxies 表.
-- LOSSY: 老 proxy_url 数据已被 backfill 改写后丢弃, rollback 只能
-- best-effort 拼回 URL (无端口字段 / 复杂转义可能失真).

BEGIN;

DROP TRIGGER IF EXISTS trg_provider_accounts_tenant_alignment ON provider_accounts;
DROP FUNCTION IF EXISTS enforce_provider_account_tenant_alignment();

DROP INDEX IF EXISTS idx_provider_accounts_tls_profile;

ALTER TABLE provider_accounts ADD COLUMN proxy_url text;

UPDATE provider_accounts pa
SET proxy_url = (
    SELECT
        p.protocol || '://' ||
        COALESCE(p.auth_username || ':' || COALESCE(p.auth_secret, '') || '@', '') ||
        p.host || ':' || p.port::text
    FROM proxies p
    WHERE p.id = pa.proxy_id AND p.deleted_at IS NULL
)
WHERE pa.proxy_id IS NOT NULL;

ALTER TABLE provider_accounts
    DROP COLUMN proxy_id,
    DROP COLUMN tls_fingerprint_profile_id;

DROP INDEX IF EXISTS idx_proxies_tenant_status_active;
DROP INDEX IF EXISTS idx_proxies_tenant_name_active;
DROP TABLE IF EXISTS proxies;

COMMIT;
