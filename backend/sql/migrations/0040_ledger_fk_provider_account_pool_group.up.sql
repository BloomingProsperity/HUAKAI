-- codex review HEAD chunk7 P1#2 + chunk12 P1: money-grade ledger 表的几个外键
-- 键列从未真加 FK → 可指向不存在或跨租户的父行。本 migration 加 composite
-- (tenant_id, ...) FK, 让 DB 层强制 DR-001 同租户不变量, 并在
-- audit_refund_pending 加 tenant_id 列。
--
-- 父表 UNIQUE (tenant_id, id) 在此 migration 先建 (0041 复用同约束);
-- billing_ledger_claims.pooling_group_id / provider_account_id 可空, NULL 时
-- composite FK (MATCH SIMPLE 默认) 自动跳过校验, 符合 reserving 阶段语义。
--
-- 如果当前生产数据有跨租户 / 孤儿引用, 该 migration 失败 — 应先 ETL 清理。

BEGIN;

-- 父表加 UNIQUE (tenant_id, id) 作为 composite FK 目标 (0041 复用)。
ALTER TABLE pool_groups
    ADD CONSTRAINT pool_groups_tenant_id_id_key UNIQUE (tenant_id, id);
ALTER TABLE provider_accounts
    ADD CONSTRAINT provider_accounts_tenant_id_id_key UNIQUE (tenant_id, id);

-- billing_ledger_claims.(tenant_id, pooling_group_id) → pool_groups(tenant_id, id)
ALTER TABLE billing_ledger_claims
    DROP CONSTRAINT IF EXISTS billing_ledger_claims_pooling_group_id_fkey;
ALTER TABLE billing_ledger_claims
    ADD CONSTRAINT billing_ledger_claims_pooling_group_id_fkey
    FOREIGN KEY (tenant_id, pooling_group_id) REFERENCES pool_groups(tenant_id, id);

-- billing_ledger_claims.(tenant_id, provider_account_id) → provider_accounts(tenant_id, id)
ALTER TABLE billing_ledger_claims
    DROP CONSTRAINT IF EXISTS billing_ledger_claims_provider_account_id_fkey;
ALTER TABLE billing_ledger_claims
    ADD CONSTRAINT billing_ledger_claims_provider_account_id_fkey
    FOREIGN KEY (tenant_id, provider_account_id) REFERENCES provider_accounts(tenant_id, id);

-- usage_records.(tenant_id, provider_account_id) → provider_accounts(tenant_id, id)
ALTER TABLE usage_records
    DROP CONSTRAINT IF EXISTS usage_records_provider_account_id_fkey;
ALTER TABLE usage_records
    ADD CONSTRAINT usage_records_provider_account_id_fkey
    FOREIGN KEY (tenant_id, provider_account_id) REFERENCES provider_accounts(tenant_id, id);

-- audit_refund_pending 加 tenant_id 列 + FK; 回填用 billing_ledger_claims 关联值。
ALTER TABLE audit_refund_pending
    ADD COLUMN IF NOT EXISTS tenant_id bigint REFERENCES tenants(id);
UPDATE audit_refund_pending arp
SET tenant_id = blc.tenant_id
FROM billing_ledger_claims blc
WHERE arp.claim_id = blc.id AND arp.tenant_id IS NULL;
ALTER TABLE audit_refund_pending
    ALTER COLUMN tenant_id SET NOT NULL;

COMMIT;
