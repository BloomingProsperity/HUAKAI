-- TLS-04: 账号级 TLS 指纹轮换开关。
-- true 时 resolver 在该 account 所属 tenant 的 active TLS profile 池里按账号
-- 确定性选一个(anti-JA3-clustering:不同账号散开、同账号粘定),取代单 FK 绑定。
-- 默认 false = 维持单绑定 / builtin 行为（零回归，opt-in）。
BEGIN;
ALTER TABLE provider_accounts
    ADD COLUMN tls_fingerprint_rotate boolean NOT NULL DEFAULT false;
COMMENT ON COLUMN provider_accounts.tls_fingerprint_rotate IS
    'HUAKAI TLS-04: true=在 tenant active TLS profile 池里按 account 确定性轮换（避免 JA3 聚集，同账号粘定）；默认 false 维持单 FK 绑定/builtin。';
COMMIT;
