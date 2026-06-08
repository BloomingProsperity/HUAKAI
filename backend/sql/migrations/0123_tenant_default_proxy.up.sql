-- PROXY-03: 租户级默认代理。账号自身无绑定(proxy_id IS NULL)时,回退到所属
-- 租户的 default_proxy_id;让运营给整租户设一个默认出口,而不必逐账号绑定。
-- 优先级 account > tenant-default > direct,每层 bound-but-unhealthy 仍 fail-closed
-- (绝不静默落到下层/直连,保账号级 IP 隔离)。可空 = 维持原 account-bound-或-直连
-- 行为(零回归,opt-in)。
BEGIN;
ALTER TABLE tenants
    ADD COLUMN default_proxy_id bigint REFERENCES proxies(id) ON DELETE SET NULL;
COMMENT ON COLUMN tenants.default_proxy_id IS
    'HUAKAI PROXY-03: 租户默认出站代理; 账号自身未绑 proxy_id 时回退到它 (仍 fail-closed); NULL=无默认.';
COMMIT;
