-- 0070_subscription_domain.up.sql
--
-- SUBSCRIPTION-1: 订阅计划、订阅订单、用户订阅实例。
-- 迁移为 additive schema；最终 landing 仍需 Owner schema 批准。

BEGIN;

CREATE TABLE IF NOT EXISTS subscription_plans (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint        NOT NULL REFERENCES tenants(id),
    code                        text          NOT NULL,
    name                        text          NOT NULL,
    description                 text          NOT NULL DEFAULT '',
    enabled                     boolean       NOT NULL DEFAULT true,
    price                       numeric(20,8) NOT NULL CHECK (price > 0),
    currency_code               char(3)       NOT NULL DEFAULT 'USD',
    duration_unit               text          NOT NULL
        CHECK (duration_unit IN ('hour','day','month','year','custom')),
    duration_value              integer       NOT NULL DEFAULT 1 CHECK (duration_value > 0),
    duration_seconds            bigint        NOT NULL DEFAULT 0 CHECK (duration_seconds >= 0),
    quota_limit                 bigint        NOT NULL DEFAULT 0 CHECK (quota_limit >= 0),
    quota_reset_period          text          NOT NULL DEFAULT 'never'
        CHECK (quota_reset_period IN ('never','daily','weekly','monthly','custom')),
    quota_reset_interval_seconds bigint       NOT NULL DEFAULT 0 CHECK (quota_reset_interval_seconds >= 0),
    max_purchases_per_user      integer       NOT NULL DEFAULT 0 CHECK (max_purchases_per_user >= 0),
    sort_order                  integer       NOT NULL DEFAULT 0,
    metadata                    jsonb         NOT NULL DEFAULT '{}'::jsonb,
    created_at                  timestamptz   NOT NULL DEFAULT now(),
    updated_at                  timestamptz   NOT NULL DEFAULT now(),
    archived_at                 timestamptz,
    CHECK (jsonb_typeof(metadata) = 'object'),
    CHECK (duration_unit <> 'custom' OR duration_seconds > 0),
    CHECK (quota_reset_period <> 'custom' OR quota_reset_interval_seconds > 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_subscription_plans_tenant_code
    ON subscription_plans (tenant_id, code)
    WHERE archived_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_subscription_plans_tenant_id
    ON subscription_plans (tenant_id, id);
CREATE INDEX IF NOT EXISTS idx_subscription_plans_tenant_enabled
    ON subscription_plans (tenant_id, enabled, sort_order, id)
    WHERE archived_at IS NULL;

CREATE TABLE IF NOT EXISTS subscription_orders (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint        NOT NULL REFERENCES tenants(id),
    user_id                     bigint        NOT NULL,
    plan_id                     bigint        NOT NULL,
    recharge_order_id           bigint        NOT NULL,
    trade_no                    text          NOT NULL,
    status                      text          NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','active','failed','expired','cancelled')),
    price                       numeric(20,8) NOT NULL CHECK (price > 0),
    currency_code               char(3)       NOT NULL DEFAULT 'USD',
    provider                    text          NOT NULL,
    plan_code                   text          NOT NULL,
    plan_name                   text          NOT NULL,
    duration_unit               text          NOT NULL
        CHECK (duration_unit IN ('hour','day','month','year','custom')),
    duration_value              integer       NOT NULL CHECK (duration_value > 0),
    duration_seconds            bigint        NOT NULL DEFAULT 0 CHECK (duration_seconds >= 0),
    quota_limit                 bigint        NOT NULL DEFAULT 0 CHECK (quota_limit >= 0),
    quota_reset_period          text          NOT NULL DEFAULT 'never'
        CHECK (quota_reset_period IN ('never','daily','weekly','monthly','custom')),
    quota_reset_interval_seconds bigint       NOT NULL DEFAULT 0 CHECK (quota_reset_interval_seconds >= 0),
    metadata                    jsonb         NOT NULL DEFAULT '{}'::jsonb,
    created_at                  timestamptz   NOT NULL DEFAULT now(),
    paid_at                     timestamptz,
    activated_at                timestamptz,
    updated_at                  timestamptz   NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id),
    FOREIGN KEY (tenant_id, plan_id) REFERENCES subscription_plans (tenant_id, id),
    FOREIGN KEY (tenant_id, recharge_order_id) REFERENCES recharge_orders (tenant_id, id),
    CHECK (jsonb_typeof(metadata) = 'object'),
    CHECK (duration_unit <> 'custom' OR duration_seconds > 0),
    CHECK (quota_reset_period <> 'custom' OR quota_reset_interval_seconds > 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_subscription_orders_tenant_trade
    ON subscription_orders (tenant_id, trade_no);
CREATE UNIQUE INDEX IF NOT EXISTS uq_subscription_orders_tenant_recharge
    ON subscription_orders (tenant_id, recharge_order_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_subscription_orders_tenant_id
    ON subscription_orders (tenant_id, id);
CREATE INDEX IF NOT EXISTS idx_subscription_orders_user_status
    ON subscription_orders (tenant_id, user_id, status, created_at DESC);

CREATE TABLE IF NOT EXISTS user_subscriptions (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint        NOT NULL REFERENCES tenants(id),
    user_id                     bigint        NOT NULL,
    plan_id                     bigint        NOT NULL,
    source_order_id             bigint,
    status                      text          NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','active','expired','cancelled')),
    quota_limit                 bigint        NOT NULL DEFAULT 0 CHECK (quota_limit >= 0),
    quota_used                  bigint        NOT NULL DEFAULT 0 CHECK (quota_used >= 0),
    quota_reset_period          text          NOT NULL DEFAULT 'never'
        CHECK (quota_reset_period IN ('never','daily','weekly','monthly','custom')),
    quota_reset_interval_seconds bigint       NOT NULL DEFAULT 0 CHECK (quota_reset_interval_seconds >= 0),
    started_at                  timestamptz,
    current_period_started_at   timestamptz,
    next_quota_reset_at         timestamptz,
    expires_at                  timestamptz,
    created_at                  timestamptz   NOT NULL DEFAULT now(),
    updated_at                  timestamptz   NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id),
    FOREIGN KEY (tenant_id, plan_id) REFERENCES subscription_plans (tenant_id, id),
    FOREIGN KEY (tenant_id, source_order_id) REFERENCES subscription_orders (tenant_id, id),
    CHECK (quota_limit = 0 OR quota_used <= quota_limit),
    CHECK (quota_reset_period <> 'custom' OR quota_reset_interval_seconds > 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_user_subscriptions_tenant_source_order
    ON user_subscriptions (tenant_id, source_order_id)
    WHERE source_order_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_user_status
    ON user_subscriptions (tenant_id, user_id, status, expires_at DESC);
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_expiry
    ON user_subscriptions (tenant_id, status, expires_at)
    WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_reset
    ON user_subscriptions (tenant_id, status, next_quota_reset_at)
    WHERE status = 'active' AND next_quota_reset_at IS NOT NULL;

COMMENT ON TABLE subscription_plans IS
    'SUBSCRIPTION-1 tenant-scoped sellable subscription plans. Additive draft pending Owner schema approval.';
COMMENT ON TABLE subscription_orders IS
    'SUBSCRIPTION-1 order snapshot linked to recharge_orders by tenant and recharge_order_id for payment audit continuity.';
COMMENT ON TABLE user_subscriptions IS
    'SUBSCRIPTION-1 user subscription instances activated from paid subscription orders.';
COMMENT ON COLUMN subscription_orders.recharge_order_id IS
    'Required link to the reused payment recharge order; callback activation must preserve this correlation.';
COMMENT ON COLUMN user_subscriptions.source_order_id IS
    'Subscription order that activated this instance; unique when present to keep callback replay idempotent.';

COMMIT;
