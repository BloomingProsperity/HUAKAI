-- HUAKAI 订阅激活地基 (Slice P3b-2)。
-- 目的: 为"支付订单购订阅 (P3b-4)"与"兑换码购订阅 (P3b-3)"建好共享地基:
--   订单/兑换码加"种类"标记 + 套餐指针, 新增订阅履约效果账本 (幂等 + 退款逆转预留),
--   并放开两处 CHECK 容纳新来源/新事件类型。本切片只建地基与事务内激活入口, 不接订单/兑换流程。
-- 红线: 订阅购买零碰 billing_events / payment_credits / 净额余额 (新机独占 money 路径)。
--   本迁移 additive only (加列 / 新表 / 放开 CHECK 加值), 不动 billing_events / payment_credits 约束。
-- 同组叠买自助只能升级（caps 逐窗口支配），降档仅管理员可操作；
-- 效果账本预留逆转列，避免退款能力落地时再次迁移。

BEGIN;

-- ----------------------------------------------------------------------------
-- payment_orders: 加订单种类 + 订阅套餐指针 (订阅订单只承载"支付凭证+计划指针",
--   权益快照随 user_subscriptions 实例 + 效果账本走, 订单不再冗余天数/caps)。
-- ----------------------------------------------------------------------------
ALTER TABLE payment_orders
    ADD COLUMN IF NOT EXISTS order_kind text NOT NULL DEFAULT 'topup',
    ADD COLUMN IF NOT EXISTS subscription_plan_id bigint;

ALTER TABLE payment_orders DROP CONSTRAINT IF EXISTS payment_orders_order_kind_check;
ALTER TABLE payment_orders ADD CONSTRAINT payment_orders_order_kind_check
    CHECK (order_kind IN ('topup', 'subscription'));

-- 订阅单必须带计划指针; 充值单不强制 (subscription_plan_id 可为空)。
ALTER TABLE payment_orders DROP CONSTRAINT IF EXISTS payment_orders_subscription_kind_check;
ALTER TABLE payment_orders ADD CONSTRAINT payment_orders_subscription_kind_check
    CHECK (order_kind <> 'subscription' OR subscription_plan_id IS NOT NULL);

-- 复合 FK (复用 subscription_plans 的 (tenant_id, id) 唯一索引); NULL 分量不触发约束。
ALTER TABLE payment_orders DROP CONSTRAINT IF EXISTS payment_orders_subscription_plan_fk;
ALTER TABLE payment_orders ADD CONSTRAINT payment_orders_subscription_plan_fk
    FOREIGN KEY (tenant_id, subscription_plan_id) REFERENCES subscription_plans (tenant_id, id);

CREATE INDEX IF NOT EXISTS idx_payment_orders_kind
    ON payment_orders (tenant_id, order_kind, status);

-- ----------------------------------------------------------------------------
-- voucher: 加授予种类 + 订阅套餐指针 (余额券行为零变更, 现存券经 DEFAULT 回填 'balance')。
-- ----------------------------------------------------------------------------
ALTER TABLE voucher
    ADD COLUMN IF NOT EXISTS grant_kind text NOT NULL DEFAULT 'balance',
    ADD COLUMN IF NOT EXISTS subscription_plan_id bigint;

ALTER TABLE voucher DROP CONSTRAINT IF EXISTS voucher_grant_kind_check;
ALTER TABLE voucher ADD CONSTRAINT voucher_grant_kind_check
    CHECK (grant_kind IN ('balance', 'subscription'));

ALTER TABLE voucher DROP CONSTRAINT IF EXISTS voucher_subscription_kind_check;
ALTER TABLE voucher ADD CONSTRAINT voucher_subscription_kind_check
    CHECK (grant_kind <> 'subscription' OR subscription_plan_id IS NOT NULL);

ALTER TABLE voucher DROP CONSTRAINT IF EXISTS voucher_subscription_plan_fk;
ALTER TABLE voucher ADD CONSTRAINT voucher_subscription_plan_fk
    FOREIGN KEY (tenant_id, subscription_plan_id) REFERENCES subscription_plans (tenant_id, id);

-- ----------------------------------------------------------------------------
-- 放开 user_subscriptions.source: 容纳 'voucher' 来源 (P3b-3 兑换码激活)。
-- ----------------------------------------------------------------------------
ALTER TABLE user_subscriptions DROP CONSTRAINT IF EXISTS user_subscriptions_source_check;
ALTER TABLE user_subscriptions ADD CONSTRAINT user_subscriptions_source_check
    CHECK (source IN ('admin', 'order', 'voucher'));

-- ----------------------------------------------------------------------------
-- 放开 subscription_audit_events.event_type: 容纳续期事件 'subscription_renewed'。
-- ----------------------------------------------------------------------------
ALTER TABLE subscription_audit_events DROP CONSTRAINT IF EXISTS subscription_audit_events_event_type_check;
ALTER TABLE subscription_audit_events ADD CONSTRAINT subscription_audit_events_event_type_check
    CHECK (event_type IN (
        'subscription_created', 'subscription_renewed', 'expired', 'cancelled',
        'group_upgraded', 'group_downgraded', 'idempotent_replay'
    ));

-- ----------------------------------------------------------------------------
-- 表: subscription_fulfillment_effects — 订阅履约效果/幂等账本 (类比 payment_credits)。
-- 一笔订单/一次兑换至多激活一条订阅 (部分唯一索引锚定); 记本次激活的精确效果,
-- 供完成态幂等重放读 + 退款逆转 (P5) 知道逆哪个套餐/扣回多少天/还原到哪个到期日。
-- 零碰钱表: 不引用 billing_events / payment_credits。
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS subscription_fulfillment_effects (
    id                     bigserial   PRIMARY KEY,
    tenant_id              bigint      NOT NULL REFERENCES tenants(id),
    -- 激活来源: order=支付订单 / voucher=兑换码 / admin=管理员手动
    source_kind            text        NOT NULL
        CHECK (source_kind IN ('order', 'voucher', 'admin')),
    payment_order_id       bigint,                          -- order 源填, 否则 NULL (幂等锚)
    voucher_redemption_id  bigint,                          -- voucher 源填, 否则 NULL (幂等锚)
    user_id                bigint      NOT NULL,
    plan_id                bigint      NOT NULL,
    user_subscription_id   bigint      NOT NULL,            -- 激活结果指向的订阅实例
    result_kind            text        NOT NULL
        CHECK (result_kind IN ('created', 'renewed')),
    applied_validity_days  integer     NOT NULL CHECK (applied_validity_days > 0),
    prev_expires_at        timestamptz,                     -- 续订前到期日 (created 时 NULL)
    new_expires_at         timestamptz NOT NULL,            -- 本次激活后到期日
    -- 退款逆转预留 (P5 才写 reversed); 本切片恒 'none'
    reversal_state         text        NOT NULL DEFAULT 'none'
        CHECK (reversal_state IN ('none', 'reversed')),
    reversed_at            timestamptz,
    created_at             timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id),
    FOREIGN KEY (tenant_id, plan_id) REFERENCES subscription_plans (tenant_id, id),
    FOREIGN KEY (tenant_id, user_subscription_id) REFERENCES user_subscriptions (tenant_id, id),
    FOREIGN KEY (tenant_id, payment_order_id) REFERENCES payment_orders (tenant_id, id),
    FOREIGN KEY (tenant_id, voucher_redemption_id) REFERENCES voucher_redemption (tenant_id, id),
    -- 来源与外键非空性一致: order 源仅 order_id, voucher 源仅 redemption_id, admin 源两者皆空
    CHECK (
        (source_kind = 'order'   AND payment_order_id IS NOT NULL AND voucher_redemption_id IS NULL) OR
        (source_kind = 'voucher' AND voucher_redemption_id IS NOT NULL AND payment_order_id IS NULL) OR
        (source_kind = 'admin'   AND payment_order_id IS NULL AND voucher_redemption_id IS NULL)
    )
);

-- 幂等锚 (部分唯一索引避免 NULL 互撞): 一订单一效果 / 一兑换一效果。
CREATE UNIQUE INDEX IF NOT EXISTS uq_sub_effect_order
    ON subscription_fulfillment_effects (tenant_id, payment_order_id)
    WHERE payment_order_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_sub_effect_voucher_redemption
    ON subscription_fulfillment_effects (tenant_id, voucher_redemption_id)
    WHERE voucher_redemption_id IS NOT NULL;
-- 反查: 按订阅实例查效果 (退款逆转/审计)。
CREATE INDEX IF NOT EXISTS idx_sub_effect_subscription
    ON subscription_fulfillment_effects (tenant_id, user_subscription_id);

COMMIT;
