-- 0170 down:退回 0092 形态(去掉 subscription_auto_renewed 事件类型与关联列)。

BEGIN;

-- 拒绝销毁 money 账本:仍有 subscription_auto_renewed 事件时回滚会使 append-only 续费账本
-- 违反旧 CHECK。RAISE 让迁移失败(事务回滚),逼运维先导出/处理,而不是静默破坏对账链。
-- 注意:被拒后 golang-migrate 会把版本标记为 dirty;数据与 schema 均完好,用
-- `migrate force 170` 恢复版本标记即可。
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM billing_events WHERE event_type = 'subscription_auto_renewed') THEN
        RAISE EXCEPTION 'refusing down 0170: % subscription_auto_renewed money events present (append-only ledger); export/handle them first',
            (SELECT count(*) FROM billing_events WHERE event_type = 'subscription_auto_renewed');
    END IF;
END
$$;

DROP INDEX IF EXISTS uq_sub_auto_renewal_billing_event;

ALTER TABLE subscription_auto_renewal_charges
    DROP COLUMN IF EXISTS billing_event_id;

DROP INDEX IF EXISTS idx_billing_events_sub_auto_renewal;

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS fk_billing_events_sub_auto_renewal_charge;

DROP INDEX IF EXISTS uq_sub_auto_renewal_tenant_id_id;

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS billing_events_event_type_check,
    ADD CONSTRAINT billing_events_event_type_check
        CHECK (event_type IN (
            'claim_committed',
            'claim_aborted',
            'reconciliation_appended',
            'voucher_redeemed',
            'balance_recharged',
            'payment_credited',
            'payment_refunded'
        ));

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS billing_events_claim_or_voucher_check,
    ADD CONSTRAINT billing_events_claim_or_voucher_check
        CHECK (
            (event_type IN ('claim_committed', 'claim_aborted', 'reconciliation_appended')
                AND claim_id IS NOT NULL
                AND voucher_redemption_id IS NULL
                AND recharge_order_id IS NULL
                AND payment_credit_id IS NULL
                AND payment_refund_id IS NULL)
            OR
            (event_type = 'voucher_redeemed'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NOT NULL
                AND recharge_order_id IS NULL
                AND payment_credit_id IS NULL
                AND payment_refund_id IS NULL)
            OR
            (event_type = 'balance_recharged'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NULL
                AND recharge_order_id IS NOT NULL
                AND payment_credit_id IS NULL
                AND payment_refund_id IS NULL)
            OR
            (event_type = 'payment_credited'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NULL
                AND recharge_order_id IS NULL
                AND payment_credit_id IS NOT NULL
                AND payment_refund_id IS NULL)
            OR
            (event_type = 'payment_refunded'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NULL
                AND recharge_order_id IS NULL
                AND payment_credit_id IS NULL
                AND payment_refund_id IS NOT NULL)
        );

ALTER TABLE billing_events
    DROP COLUMN IF EXISTS subscription_auto_renewal_charge_id;

COMMIT;
