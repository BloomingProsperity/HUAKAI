-- 0053 down:回到 0050 的 7-value event_kind CHECK。
--
-- 关键安全:回滚前必须确认没有 post_delivery_settlement 行存在 —— 否则
-- CHECK 收紧后该列原值不再合法,所有该行的 SELECT / UPDATE 都会因 row
-- 不符合 CHECK 而报错(PostgreSQL CHECK 对 existing rows 在 ALTER 时
-- 验证,有 row 会直接 ALTER 失败)。本 DO 块预先 RAISE EXCEPTION,
-- 让 ops 看到具体阻塞原因,而不是猜 ALTER 失败信息。

BEGIN;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM usage_record_dlq WHERE event_kind = 'post_delivery_settlement') THEN
        RAISE EXCEPTION 'cannot rollback 0053: post_delivery_settlement DLQ rows exist; drain or quarantine them first';
    END IF;
END $$;

ALTER TABLE usage_record_dlq
    DROP CONSTRAINT IF EXISTS usage_record_dlq_event_kind_check,
    ADD CONSTRAINT usage_record_dlq_event_kind_check
        CHECK (event_kind IN
            ('usage_record', 'billing_event_replica', 'audit_event_replica',
             'audit_mismatch_refund', 'account_health', 'metrics',
             'audit_ledger_entry'));
COMMIT;
