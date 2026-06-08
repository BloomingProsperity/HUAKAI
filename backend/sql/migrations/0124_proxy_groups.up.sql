-- PROXY-05: 代理池轮换。proxies.group_id 给代理打组标签;provider_accounts
-- 绑定 proxy_group_id(而非单个 proxy_id)时,resolver 在该组 active 成员里按
-- 账号【确定性】选一个(fnv(accountID)%N:不同账号散布到池里不同 IP=反聚类,
-- 同账号永远粘同一个=保住住宅IP稳定的隔离契约)。空组/无健康成员 fail-closed。
-- 优先级 account 单绑 > account 组轮换 > tenant-default > direct。两列均可空 =
-- inert-until-set,零回归。
BEGIN;
ALTER TABLE proxies ADD COLUMN group_id text;
ALTER TABLE provider_accounts ADD COLUMN proxy_group_id text;
CREATE INDEX idx_proxies_group ON proxies (tenant_id, group_id)
    WHERE group_id IS NOT NULL AND deleted_at IS NULL;
COMMENT ON COLUMN proxies.group_id IS 'HUAKAI PROXY-05: 代理组标签; 同组成员可被账号轮换选取。';
COMMENT ON COLUMN provider_accounts.proxy_group_id IS 'HUAKAI PROXY-05: 账号绑定的代理组; resolver 在组内 active 成员按账号确定性轮换 (sticky-by-account)。';
COMMIT;
