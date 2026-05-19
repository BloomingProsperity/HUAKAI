-- codex review HEAD chunk7 P1#2: money-grade ledger 表的几个外键键列从未真
-- 加 FK 约束 → 可指向不存在或跨租户的父行。本 migration 把以下三处真接
-- FK, 并在 audit_refund_pending 加 tenant_id 谓词列。
--
-- 影响表:
--   billing_ledger_claims (pooling_group_id, provider_account_id)
--   usage_records (provider_account_id)
--   audit_refund_pending (新加 tenant_id 列 + FK)
--
-- 如果当前生产数据有指向已删行的孤儿引用, 该 migration 失败 — 应先 ETL
-- 清理再升级 (production 跑前 ops 手动 dry-run)。

BEGIN;

-- billing_ledger_claims.pooling_group_id → pool_groups(id)
ALTER TABLE billing_ledger_claims
    DROP CONSTRAINT IF EXISTS billing_ledger_claims_pooling_group_id_fkey;
ALTER TABLE billing_ledger_claims
    ADD CONSTRAINT billing_ledger_claims_pooling_group_id_fkey
    FOREIGN KEY (pooling_group_id) REFERENCES pool_groups(id);

-- billing_ledger_claims.provider_account_id → provider_accounts(id)
ALTER TABLE billing_ledger_claims
    DROP CONSTRAINT IF EXISTS billing_ledger_claims_provider_account_id_fkey;
ALTER TABLE billing_ledger_claims
    ADD CONSTRAINT billing_ledger_claims_provider_account_id_fkey
    FOREIGN KEY (provider_account_id) REFERENCES provider_accounts(id);

-- usage_records.provider_account_id → provider_accounts(id)
ALTER TABLE usage_records
    DROP CONSTRAINT IF EXISTS usage_records_provider_account_id_fkey;
ALTER TABLE usage_records
    ADD CONSTRAINT usage_records_provider_account_id_fkey
    FOREIGN KEY (provider_account_id) REFERENCES provider_accounts(id);

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
