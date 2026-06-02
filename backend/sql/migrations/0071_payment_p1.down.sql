-- 0071 支付子系统 Slice P1 回滚。
-- 安全: 若已有 payment 钱数据 (billing_events payment_credited 或 payment_credits 非空),
-- 拒绝回滚 — 不静默删除钱台账; 带数据的生产回滚必须另走 Owner-gated 数据归档计划。

BEGIN;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM billing_events WHERE event_type = 'payment_credited')
       OR EXISTS (SELECT 1 FROM payment_credits) THEN
        RAISE EXCEPTION 'refusing to roll back 0071: payment money data exists (billing_events payment_credited / payment_credits non-empty); production rollback must go through an Owner-gated data plan';
    END IF;
END $$;

-- 先撤 billing_events 对 payment_credits 的 FK 与索引, 再恢复 0023 版约束
ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS fk_billing_events_payment_credit;
DROP INDEX IF EXISTS idx_billing_events_payment_credit;

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS billing_events_claim_or_voucher_check,
    ADD CONSTRAINT billing_events_claim_or_voucher_check
        CHECK (
            (event_type IN ('claim_committed', 'claim_aborted', 'reconciliation_appended')
                AND claim_id IS NOT NULL
                AND voucher_redemption_id IS NULL
                AND recharge_order_id IS NULL)
            OR
            (event_type = 'voucher_redeemed'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NOT NULL
                AND recharge_order_id IS NULL)
            OR
            (event_type = 'balance_recharged'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NULL
                AND recharge_order_id IS NOT NULL)
        );

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS billing_events_event_type_check,
    ADD CONSTRAINT billing_events_event_type_check
        CHECK (event_type IN (
            'claim_committed',
            'claim_aborted',
            'reconciliation_appended',
            'voucher_redeemed',
            'balance_recharged'
        ));

ALTER TABLE billing_events
    DROP COLUMN IF EXISTS payment_credit_id;

-- 删表顺序: 先删引用 payment_orders 的子表, 再删 payment_orders
DROP TABLE IF EXISTS payment_audit_events;
DROP TABLE IF EXISTS payment_credits;
DROP TABLE IF EXISTS payment_orders;

COMMIT;
