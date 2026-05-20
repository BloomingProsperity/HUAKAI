-- codex review v18 P1: 0044 的 idempotency_replay_records.claim_id 原为单列
-- FK → billing_ledger_claims(id), 不带 tenant 维度 — 坏写入可把 B 租户关联
-- 到 A 租户的 claim。 v17 本应改复合 FK; 但直接改 0044 建表语句, 已应用 0044
-- 的库 golang-migrate 不会重跑, 旧库保留单列 FK、新库得复合 FK, 造成分叉。
--
-- 本迁移以独立 ALTER 把 FK 升级为复合 (tenant_id, claim_id) → billing_ledger_
-- claims(tenant_id, id), DB 层强制同租户绑定 (与 0040/0041 其它 billing 子表
-- 同模式)。 父表 uq_billing_ledger_claims_tenant_id_id 唯一索引 (0009) 作 FK
-- 目标。 已应用与全新库执行本迁移后最终 schema 一致。
--
-- 假设: 现有数据 tenant_id 与 claim 一致 (pre-prod)。 跨租户行存在则 ALTER
-- fail-fast, 应先 ETL 清理。

BEGIN;

ALTER TABLE idempotency_replay_records
    DROP CONSTRAINT IF EXISTS idempotency_replay_records_claim_id_fkey;
ALTER TABLE idempotency_replay_records
    ADD CONSTRAINT idempotency_replay_records_claim_id_fkey
    FOREIGN KEY (tenant_id, claim_id) REFERENCES billing_ledger_claims (tenant_id, id);

COMMIT;
