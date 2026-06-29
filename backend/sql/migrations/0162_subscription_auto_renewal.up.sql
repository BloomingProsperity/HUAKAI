-- HUAKAI 订阅自动续费账本 (Slice P-AUTORENEW)。
-- 目的: 为"扫到期 → 扣钱包余额 → 续期"的后台 worker 建一张专表, 它同时承担两职责:
--   (1) 幂等锚: 唯一索引 (tenant_id, user_subscription_id, period_key) 防 worker 重跑 / 应用层重试
--       对同一到期周期重复扣费 (period_key = 续费前订阅 expires_at 的 RFC3339, 一个窗口只续一次)。
--   (2) money-movement 审计行: 记本次续费扣了多少 (amount_cents)、续期前后到期日, 供运维对账。
-- 红线 (与 0075 同纪律): 本迁移 additive only —— 只建新表 + 新唯一索引, 绝不动 billing_events /
--   payment_credits / user_balances 的任何现有约束。续费扣款扣的是可变钱包表 user_balances
--   (与退款扣款同表同形态), 本表只记账不持有余额。

BEGIN;

CREATE TABLE IF NOT EXISTS subscription_auto_renewal_charges (
    id                    bigserial   PRIMARY KEY,
    tenant_id             bigint      NOT NULL REFERENCES tenants(id),
    user_id               bigint      NOT NULL,
    user_subscription_id  bigint      NOT NULL,
    -- 续费周期标识 = 本次续费前订阅的 expires_at (RFC3339); 同一窗口幂等只扣一次。
    period_key            text        NOT NULL,
    plan_id               bigint      NOT NULL,
    -- 本次续费扣减的钱包金额 (cents, >=0; 0 表示免费续费 plan.price_cents<=0)。
    amount_cents          bigint      NOT NULL CHECK (amount_cents >= 0),
    -- 续期前后到期日快照 (审计 / 未来逆转用)。
    prev_expires_at       timestamptz NOT NULL,
    new_expires_at        timestamptz NOT NULL,
    created_at            timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id),
    FOREIGN KEY (tenant_id, user_subscription_id) REFERENCES user_subscriptions (tenant_id, id),
    FOREIGN KEY (tenant_id, plan_id) REFERENCES subscription_plans (tenant_id, id)
);

-- 幂等锚: 一个 (订阅, 周期) 至多一条续费扣款行。worker 重跑撞此索引返回 23505 → 回滚跳过。
CREATE UNIQUE INDEX IF NOT EXISTS uq_sub_auto_renewal_period
    ON subscription_auto_renewal_charges (tenant_id, user_subscription_id, period_key);

-- 反查: 按用户列续费历史 (账单 / 审计)。
CREATE INDEX IF NOT EXISTS idx_sub_auto_renewal_user
    ON subscription_auto_renewal_charges (tenant_id, user_id, created_at);

COMMIT;
