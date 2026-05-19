-- 0037_tls_fingerprint_profiles.down.sql
-- 反向: 删 TLS 模板表 + 索引。provider_accounts.tls_fingerprint_profile_id
-- FK 由 0038 加, 完整 rollback 顺序应先跑 0038.down 解 FK 再跑本文件。

BEGIN;

DROP INDEX IF EXISTS idx_tls_fingerprint_profiles_tenant_status_active;
DROP INDEX IF EXISTS idx_tls_fingerprint_profiles_tenant_name_active;
DROP TABLE IF EXISTS tls_fingerprint_profiles;

COMMIT;
