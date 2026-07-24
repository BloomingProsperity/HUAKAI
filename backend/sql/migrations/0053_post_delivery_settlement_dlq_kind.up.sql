-- 0053 加 post_delivery_settlement 到 usage_record_dlq.event_kind CHECK。
--
-- 流式或非流式直接结算失败后，把“已交付但未结算”状态转成 durable DLQ intent，
-- worker 重调 Settler.Settle 并通过原幂等路径重放。
--
-- pattern 跟 0050_dlq_audit_ledger_entry_kind 完全一致:DROP + ADD CHECK,
-- 不重写数据;数据库锁是 EXCLUSIVE ACCESS 但秒级完成。Owner schema-gate
-- 已批 (2026-05-24)。

BEGIN;
ALTER TABLE usage_record_dlq
    DROP CONSTRAINT IF EXISTS usage_record_dlq_event_kind_check,
    ADD CONSTRAINT usage_record_dlq_event_kind_check
        CHECK (event_kind IN
            ('usage_record', 'billing_event_replica', 'audit_event_replica',
             'audit_mismatch_refund', 'account_health', 'metrics',
             'audit_ledger_entry', 'post_delivery_settlement'));
COMMIT;
