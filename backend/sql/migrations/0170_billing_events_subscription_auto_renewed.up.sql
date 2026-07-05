-- 0170_billing_events_subscription_auto_renewed.up.sql
--
-- 订阅自动续费扣款进入统一 money 事件流 billing_events(与充值/退款/兑换同账本),
-- 补齐对账链:此前续费只写 subscription_auto_renewal_charges 专表,统一账本缺一环。
-- 符号约定沿用钱包流出先例(payment_refunded):actual_cost=0,actual_cost_signed=-金额。

BEGIN;

ALTER TABLE billing_events
    ADD COLUMN IF NOT EXISTS subscription_auto_renewal_charge_id bigint;

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
            'payment_refunded',
            'subscription_auto_renewed'
        ));

-- 事件类型与关联列一一配对(每类恰好带自己的 ref,其余必须 NULL)。
ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS billing_events_claim_or_voucher_check,
    ADD CONSTRAINT billing_events_claim_or_voucher_check
        CHECK (
            (event_type IN ('claim_committed', 'claim_aborted', 'reconciliation_appended')
                AND claim_id IS NOT NULL
                AND voucher_redemption_id IS NULL
                AND recharge_order_id IS NULL
                AND payment_credit_id IS NULL
                AND payment_refund_id IS NULL
                AND subscription_auto_renewal_charge_id IS NULL)
            OR
            (event_type = 'voucher_redeemed'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NOT NULL
                AND recharge_order_id IS NULL
                AND payment_credit_id IS NULL
                AND payment_refund_id IS NULL
                AND subscription_auto_renewal_charge_id IS NULL)
            OR
            (event_type = 'balance_recharged'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NULL
                AND recharge_order_id IS NOT NULL
                AND payment_credit_id IS NULL
                AND payment_refund_id IS NULL
                AND subscription_auto_renewal_charge_id IS NULL)
            OR
            (event_type = 'payment_credited'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NULL
                AND recharge_order_id IS NULL
                AND payment_credit_id IS NOT NULL
                AND payment_refund_id IS NULL
                AND subscription_auto_renewal_charge_id IS NULL)
            OR
            (event_type = 'payment_refunded'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NULL
                AND recharge_order_id IS NULL
                AND payment_credit_id IS NULL
                AND payment_refund_id IS NOT NULL
                AND subscription_auto_renewal_charge_id IS NULL)
            OR
            (event_type = 'subscription_auto_renewed'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NULL
                AND recharge_order_id IS NULL
                AND payment_credit_id IS NULL
                AND payment_refund_id IS NULL
                AND subscription_auto_renewal_charge_id IS NOT NULL)
        );

-- 复合 FK 目标需要 (tenant_id, id) 唯一。
CREATE UNIQUE INDEX IF NOT EXISTS uq_sub_auto_renewal_tenant_id_id
    ON subscription_auto_renewal_charges (tenant_id, id);

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS fk_billing_events_sub_auto_renewal_charge,
    ADD CONSTRAINT fk_billing_events_sub_auto_renewal_charge
        FOREIGN KEY (tenant_id, subscription_auto_renewal_charge_id)
        REFERENCES subscription_auto_renewal_charges (tenant_id, id);

CREATE INDEX IF NOT EXISTS idx_billing_events_sub_auto_renewal
    ON billing_events (tenant_id, subscription_auto_renewal_charge_id)
    WHERE subscription_auto_renewal_charge_id IS NOT NULL;

-- 回链:续费账本行 → 它的 billing_events 行(与 payment_credits.billing_event_id 同形态)。
ALTER TABLE subscription_auto_renewal_charges
    ADD COLUMN IF NOT EXISTS billing_event_id bigint;

-- 一条 billing_events 行不被多条 charge 复用(与 payment_credits 回链同纪律)。
CREATE UNIQUE INDEX IF NOT EXISTS uq_sub_auto_renewal_billing_event
    ON subscription_auto_renewal_charges (tenant_id, billing_event_id)
    WHERE billing_event_id IS NOT NULL;

COMMIT;
