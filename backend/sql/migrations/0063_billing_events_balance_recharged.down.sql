-- 0063_billing_events_balance_recharged.down.sql
--
-- DEV ROLLBACK ONLY. Restores the billing_events CHECK shape from
-- 0023_voucher_system after removing MONEY-4 recharge event linkage.

BEGIN;

-- 拒绝销毁 money 账本:仍有 balance_recharged 事件时,本 DEV-only 回滚会删掉 append-only
-- 充值账本证据。直接 RAISE 让迁移失败(事务回滚),逼运维先导出/处理,而不是静默删钱。
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM billing_events WHERE event_type = 'balance_recharged') THEN
        RAISE EXCEPTION 'refusing down 0063: % balance_recharged money events present (would delete append-only ledger); export/handle them first',
            (SELECT count(*) FROM billing_events WHERE event_type = 'balance_recharged');
    END IF;
END
$$;

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS fk_billing_events_recharge_order;

DROP INDEX IF EXISTS idx_billing_events_recharge_order;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM pg_trigger
        WHERE tgrelid = 'billing_events'::regclass
          AND tgname = 'billing_events_append_only_delete'
          AND NOT tgisinternal
    ) THEN
        EXECUTE 'ALTER TABLE billing_events DISABLE TRIGGER billing_events_append_only_delete';
    END IF;
END
$$;

DELETE FROM billing_events
WHERE event_type = 'balance_recharged';

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM pg_trigger
        WHERE tgrelid = 'billing_events'::regclass
          AND tgname = 'billing_events_append_only_delete'
          AND NOT tgisinternal
    ) THEN
        EXECUTE 'ALTER TABLE billing_events ENABLE TRIGGER billing_events_append_only_delete';
    END IF;
END
$$;

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS billing_events_claim_or_voucher_check,
    ADD CONSTRAINT billing_events_claim_or_voucher_check
        CHECK (
            (event_type IN ('claim_committed', 'claim_aborted', 'reconciliation_appended')
                AND claim_id IS NOT NULL
                AND voucher_redemption_id IS NULL)
            OR
            (event_type = 'voucher_redeemed'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NOT NULL)
        );

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS billing_events_event_type_check,
    ADD CONSTRAINT billing_events_event_type_check
        CHECK (event_type IN (
            'claim_committed',
            'claim_aborted',
            'reconciliation_appended',
            'voucher_redeemed'
        ));

ALTER TABLE billing_events
    DROP COLUMN IF EXISTS recharge_order_id;

COMMIT;
