-- HUAKAI 支付子系统 Slice P1 schema。
-- 内部支付机器: 订单状态机 + 一单一入账 + 领域审计 + billing_events 入账 seam。
-- 复刻 voucher 的入账模式: 余额走 billing_events 派生 SUM, 不落独立可变余额表;
-- 真实支付渠道 SDK 不在 P1, 留 Owner-gated 后续切片。
-- 所有表以 tenant_id 作为隔离键, 金额用 amount_cents bigint (对齐 voucher_redemption),
-- billing_events.actual_cost 仍以 dollars numeric 记 (amount_cents/100)。
-- 与新机协调点: 本 migration DROP+ADD billing_events 的 event_type CHECK 与互斥约束,
-- 当前 (截至 0070) 这两个约束仅 0023 定义过, 主线合并前须确认新机未并发改同约束。

BEGIN;

-- ----------------------------------------------------------------------------
-- 表: payment_orders — 订单状态机主体
-- 状态: pending -> paid -> recharging -> completed; 旁路 expired/cancelled/failed
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS payment_orders (
    id                     bigserial   PRIMARY KEY,
    tenant_id              bigint      NOT NULL REFERENCES tenants(id),
    user_id                bigint      NOT NULL,
    out_trade_no           text        NOT NULL,
    amount_cents           bigint      NOT NULL CHECK (amount_cents > 0),
    currency_code          char(3)     NOT NULL DEFAULT 'USD',
    status                 text        NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'paid', 'recharging', 'completed', 'expired', 'cancelled', 'failed')),
    provider_kind          text        NOT NULL DEFAULT 'manual'
        CHECK (provider_kind IN ('manual', 'test', 'hmac')),
    provider_order_ref     text,
    provider_snapshot      jsonb,
    request_fingerprint    text,
    created_by_admin_id    bigint,
    confirmed_by_admin_id  bigint,
    confirm_reason         text,
    failure_code           text,
    failure_message        text,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    expires_at             timestamptz,
    paid_at                timestamptz,
    recharging_at          timestamptz,
    completed_at           timestamptz,
    failed_at              timestamptz,
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)
);

-- 重复外部订单号在同租户内只能有一张订单 (幂等第一重)
CREATE UNIQUE INDEX IF NOT EXISTS uq_payment_orders_out_trade_no
    ON payment_orders (tenant_id, out_trade_no);
-- 复合唯一索引: 供 payment_credits / payment_audit_events 的 tenant-scoped 复合 FK 引用
CREATE UNIQUE INDEX IF NOT EXISTS uq_payment_orders_tenant_id
    ON payment_orders (tenant_id, id);
CREATE INDEX IF NOT EXISTS idx_payment_orders_tenant_status
    ON payment_orders (tenant_id, status, updated_at);
CREATE INDEX IF NOT EXISTS idx_payment_orders_user_time
    ON payment_orders (tenant_id, user_id, created_at DESC);

-- ----------------------------------------------------------------------------
-- 表: payment_credits — 已入账事实, 一张订单最多一条 (不是余额表)
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS payment_credits (
    id                bigserial   PRIMARY KEY,
    tenant_id         bigint      NOT NULL REFERENCES tenants(id),
    payment_order_id  bigint      NOT NULL,
    user_id           bigint      NOT NULL,
    amount_cents      bigint      NOT NULL CHECK (amount_cents > 0),
    currency_code     char(3)     NOT NULL DEFAULT 'USD',
    reason_class      text        NOT NULL DEFAULT 'manual_confirmed'
        CHECK (reason_class IN ('manual_confirmed', 'test_provider_paid')),
    billing_event_id  bigint,
    created_at        timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, payment_order_id) REFERENCES payment_orders (tenant_id, id),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)
);

-- 一单一入账 (幂等第三重, 兜底并发履约不双账)
CREATE UNIQUE INDEX IF NOT EXISTS uq_payment_credits_order
    ON payment_credits (tenant_id, payment_order_id);
-- 复合唯一索引: 供 billing_events 的 tenant-scoped 复合 FK 引用
CREATE UNIQUE INDEX IF NOT EXISTS uq_payment_credits_tenant_id
    ON payment_credits (tenant_id, id);
-- 一条 billing_event 不被多个 credit 复用
CREATE UNIQUE INDEX IF NOT EXISTS uq_payment_credits_billing_event
    ON payment_credits (tenant_id, billing_event_id)
    WHERE billing_event_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_payment_credits_user_time
    ON payment_credits (tenant_id, user_id, created_at DESC);

-- ----------------------------------------------------------------------------
-- 表: payment_audit_events — 钱路径操作审计 (Owner 2026-05-29 决策 A: 独立领域审计表)
-- 与 billing_events 职责分离: billing_events 记成功入账的钱事实(不可变台账),
-- 本表记操作轨迹(谁建单/谁确认/为何/失败/重放); 幂等不依赖本表(靠 unique+CAS), 纯观测。
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS payment_audit_events (
    id                bigserial   PRIMARY KEY,
    tenant_id         bigint      NOT NULL REFERENCES tenants(id),
    payment_order_id  bigint      NOT NULL,
    event_type        text        NOT NULL
        CHECK (event_type IN (
            'order_created', 'paid_confirmed', 'fulfillment_started', 'credited',
            'fulfillment_failed', 'idempotent_replay', 'order_expired', 'order_cancelled')),
    actor_kind        text        NOT NULL DEFAULT 'system'
        CHECK (actor_kind IN ('admin', 'user', 'system')),
    actor_id          bigint,
    reason_class      text,
    request_id        text,
    redacted_payload  jsonb,
    occurred_at       timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, payment_order_id) REFERENCES payment_orders (tenant_id, id)
);

CREATE INDEX IF NOT EXISTS idx_payment_audit_order
    ON payment_audit_events (tenant_id, payment_order_id, occurred_at);

-- ----------------------------------------------------------------------------
-- billing_events 入账 seam 扩展 (与 0023 voucher 同模式, 加 payment 第三分支)
-- ----------------------------------------------------------------------------
ALTER TABLE billing_events
    ADD COLUMN IF NOT EXISTS payment_credit_id bigint;

-- 事件类型白名单: 保留 0023 四值, 追加 payment_credited
ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS billing_events_event_type_check,
    ADD CONSTRAINT billing_events_event_type_check
        CHECK (event_type IN (
            'claim_committed',
            'claim_aborted',
            'reconciliation_appended',
            'voucher_redeemed',
            'balance_recharged',
            'payment_credited'
        ));

-- 互斥约束扩成四分支: claim 类 / voucher / legacy recharge / payment 四者互斥。
ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS billing_events_claim_or_voucher_check,
    ADD CONSTRAINT billing_events_claim_or_voucher_check
        CHECK (
            (event_type IN ('claim_committed', 'claim_aborted', 'reconciliation_appended')
                AND claim_id IS NOT NULL
                AND voucher_redemption_id IS NULL
                AND recharge_order_id IS NULL
                AND payment_credit_id IS NULL)
            OR
            (event_type = 'voucher_redeemed'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NOT NULL
                AND recharge_order_id IS NULL
                AND payment_credit_id IS NULL)
            OR
            (event_type = 'balance_recharged'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NULL
                AND recharge_order_id IS NOT NULL
                AND payment_credit_id IS NULL)
            OR
            (event_type = 'payment_credited'
                AND claim_id IS NULL
                AND voucher_redemption_id IS NULL
                AND recharge_order_id IS NULL
                AND payment_credit_id IS NOT NULL)
        );

ALTER TABLE billing_events
    DROP CONSTRAINT IF EXISTS fk_billing_events_payment_credit,
    ADD CONSTRAINT fk_billing_events_payment_credit
        FOREIGN KEY (tenant_id, payment_credit_id)
        REFERENCES payment_credits (tenant_id, id);

CREATE INDEX IF NOT EXISTS idx_billing_events_payment_credit
    ON billing_events (tenant_id, payment_credit_id)
    WHERE payment_credit_id IS NOT NULL;

COMMIT;
