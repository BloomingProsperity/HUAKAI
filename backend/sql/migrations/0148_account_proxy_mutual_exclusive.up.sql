-- 0148_account_proxy_mutual_exclusive.up.sql
-- 账号代理绑定互斥(防 IP 隔离泄漏的 DB 层防御纵深)。
--
-- provider_accounts 有两种出站代理绑定:proxy_id(单绑某代理,1:1)与
-- proxy_group_id(绑代理组,resolver 在组内按账号确定性轮换=住宅 IP 隔离)。
-- 二者【不可同时有值】:resolver(postgres_proxy_resolver)优先级 proxy_id > 组,
-- 若两列都设,组绑定被静默忽略 → 破坏运维三档语义(直连/指定代理/代理组)、
-- 可能让账号走错 IP、漏掉本应的隔离意图。
--
-- handler 写路径已强制互斥(设一列清另一列);本 CHECK 是 DB 层兜底,防裸 SQL /
-- 未来其它写入路径绕过应用层。空字符串 group_id 视同未设(与 resolver 的
-- proxy_group_id <> '' 判定一致)。
--
-- 注:列已存在(proxy_id@0038 / proxy_group_id@0124);当前无 admin 写路径,故现有行
-- 不会两列同设,约束可干净添加。
-- (迁移号 0148:operator-switches 分支也占用了 0148 做 proxy_fallback_mode,二者合并时
--  需重编号其一——已知小冲突。)

ALTER TABLE provider_accounts
    ADD CONSTRAINT chk_account_proxy_mutual_exclusive CHECK (
        NOT (proxy_id IS NOT NULL AND proxy_group_id IS NOT NULL AND proxy_group_id <> '')
    );
