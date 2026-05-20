-- L2 response cache 命中是"成功但无上游账号"的 $0 请求: 它有 claim 行 (幂等
-- 需要) 但没有 provider account、没有 pool slot token, 故 usage_records 原先
-- 写不进去 (provider_account_id / acquisition_token 是 NOT NULL)。 缓存命中不写
-- usage 行, 导致 receipt / admin 用量 / obs / 退款 等下游一致地拿不到 cache-hit
-- 事实 (codex review v15 P2-2)。
--
-- 本迁移把这两列改为"受约束可空": 新增 settlement_source 判别列 ——
--   provider_upstream  路径: 两列必须非空 (原同租户账号不变量一点不松)
--   response_cache_l2  路径: 两列必须为空
-- CHECK 同时封死 settlement_source 值域 (其它值两支都不满足即被拒)。
--
-- 0040 的复合 FK (tenant_id, provider_account_id) → provider_accounts 是
-- MATCH SIMPLE (默认): provider_account_id 为 NULL 时整条 FK 跳过校验,
-- 故 cache-hit 行无需 provider, FK 无需改动。
--
-- 现存行 settlement_source 经 DEFAULT 全部回填 provider_upstream (正确 ——
-- 迁移前全部是上游路径), 且其 provider_account_id / acquisition_token 本就
-- 非空, 满足 CHECK 第一支。

BEGIN;

ALTER TABLE usage_records
    ADD COLUMN IF NOT EXISTS settlement_source text NOT NULL DEFAULT 'provider_upstream';

ALTER TABLE usage_records
    ALTER COLUMN provider_account_id DROP NOT NULL;
ALTER TABLE usage_records
    ALTER COLUMN acquisition_token DROP NOT NULL;

ALTER TABLE usage_records
    ADD CONSTRAINT usage_records_settlement_source_chk CHECK (
        (settlement_source = 'provider_upstream'
            AND provider_account_id IS NOT NULL
            AND acquisition_token IS NOT NULL)
        OR
        (settlement_source = 'response_cache_l2'
            AND provider_account_id IS NULL
            AND acquisition_token IS NULL)
    );

COMMIT;
