-- HUAKAI 订阅子系统 schema。
-- 订阅只给配额套餐 (不充余额, 不碰 payment_credits/billing_events 钱表),
--   每周期自动续 (窗口化重置), 含用户分组升级/到期降级。
-- HUAKAI 订阅模型: 计划绑用户路由组 + 窗口化日/周/月 USD 花费上限 + validity 周期 + 到期降级。
-- HUAKAI 订阅行为:
--   1) 上限不在订阅行里另存计数器, 而是激活时按窗口装进统一 internal/quota 引擎
--      (cost_usd 策略: daily_cap->calendar_day / weekly_cap->calendar_week / monthly_cap->calendar_month,
--       valid_from=starts_at, valid_until=expires_at)。引擎按日历边界自动重置 -> 不需周期 worker。
--   2) subscription_policy_links 记录订阅与其 quota 策略的所有权, 到期/取消时关闭对应策略。
--   3) PRIMARY entitlement = users.user_group (路由访问); cap 是 guardrail; 到期降级 = 真正停服。
-- 所有表以 tenant_id 隔离; 金额上限用 numeric(20,8) USD (对齐 quota_policies.limit_value)。

BEGIN;

-- ----------------------------------------------------------------------------
-- users.user_group — 用户当前路由分组 (默认 'default'; 订阅激活升 premium 等, 到期还原)。
-- 此前 routes.user_group_match (0001) 已能按组路由, 但缺"用户属于哪组"的存储, 本列补齐。
-- ----------------------------------------------------------------------------
ALTER TABLE users ADD COLUMN IF NOT EXISTS user_group text NOT NULL DEFAULT 'default';

-- ----------------------------------------------------------------------------
-- 表: subscription_plans — 订阅套餐目录 (配额型)
-- 三档 cap 任意可空: 非空者激活时各装一条对应日历窗口的 cost_usd 策略 (可同时多档)。
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS subscription_plans (
    id                bigserial   PRIMARY KEY,
    tenant_id         bigint      NOT NULL REFERENCES tenants(id),
    name              text        NOT NULL,
    description       text        NOT NULL DEFAULT '',
    -- 目录价 (quota-only 下仅展示 / 支付购买用; 不入余额账本)
    price_cents       bigint      NOT NULL DEFAULT 0 CHECK (price_cents >= 0),
    currency_code     char(3)     NOT NULL DEFAULT 'USD',
    -- 订阅有效期天数
    validity_days     integer     NOT NULL CHECK (validity_days > 0),
    -- 本套餐授予的用户路由分组 (写入 users.user_group; 空串=不改组)
    granted_group     text        NOT NULL DEFAULT '',
    -- 窗口化 USD 花费上限 (NULL = 该窗口不设限); 激活时映射为 internal/quota cost_usd 日历窗口策略
    daily_cap_usd     numeric(20,8) CHECK (daily_cap_usd IS NULL OR daily_cap_usd >= 0),
    weekly_cap_usd    numeric(20,8) CHECK (weekly_cap_usd IS NULL OR weekly_cap_usd >= 0),
    monthly_cap_usd   numeric(20,8) CHECK (monthly_cap_usd IS NULL OR monthly_cap_usd >= 0),
    for_sale          boolean     NOT NULL DEFAULT true,
    enabled           boolean     NOT NULL DEFAULT true,
    sort_order        integer     NOT NULL DEFAULT 0,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
-- 复合唯一索引: 供 user_subscriptions tenant-scoped 复合 FK 引用
CREATE UNIQUE INDEX IF NOT EXISTS uq_subscription_plans_tenant_id
    ON subscription_plans (tenant_id, id);
CREATE INDEX IF NOT EXISTS idx_subscription_plans_sale
    ON subscription_plans (tenant_id, for_sale, enabled, sort_order);

-- ----------------------------------------------------------------------------
-- 表: user_subscriptions — 用户订阅实例 (plan 快照: 计划改动不回溯影响已购订阅)
-- 状态机: active -> expired / cancelled
-- 配额重置由 quota 引擎日历窗口自动完成, 本表不存周期游标。
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS user_subscriptions (
    id                    bigserial   PRIMARY KEY,
    tenant_id             bigint      NOT NULL REFERENCES tenants(id),
    user_id               bigint      NOT NULL,
    plan_id               bigint      NOT NULL,
    -- plan 快照
    granted_group         text        NOT NULL DEFAULT '',
    daily_cap_usd         numeric(20,8) CHECK (daily_cap_usd IS NULL OR daily_cap_usd >= 0),
    weekly_cap_usd        numeric(20,8) CHECK (weekly_cap_usd IS NULL OR weekly_cap_usd >= 0),
    monthly_cap_usd       numeric(20,8) CHECK (monthly_cap_usd IS NULL OR monthly_cap_usd >= 0),
    status                text        NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'expired', 'cancelled')),
    source                text        NOT NULL DEFAULT 'admin'
        CHECK (source IN ('admin', 'order')),
    assigned_by_admin_id  bigint,
    -- 到期降级还原用: 激活时用户的原分组
    prev_user_group       text        NOT NULL DEFAULT 'default',
    starts_at             timestamptz NOT NULL DEFAULT now(),
    expires_at            timestamptz NOT NULL,
    cancelled_at          timestamptz,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at > starts_at),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id),
    FOREIGN KEY (tenant_id, plan_id) REFERENCES subscription_plans (tenant_id, id)
);
-- 复合唯一索引: 供 subscription_audit_events / subscription_policy_links 复合 FK 引用
CREATE UNIQUE INDEX IF NOT EXISTS uq_user_subscriptions_tenant_id
    ON user_subscriptions (tenant_id, id);
-- 同租户同用户同组只能有一条 active 订阅 (幂等授予 + 防重复升组)
CREATE UNIQUE INDEX IF NOT EXISTS uq_user_subscriptions_active_group
    ON user_subscriptions (tenant_id, user_id, granted_group)
    WHERE status = 'active';
-- worker: 到期扫描 (active 且 expires_at 到点)
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_due_expiry
    ON user_subscriptions (tenant_id, expires_at)
    WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_user
    ON user_subscriptions (tenant_id, user_id, status);

-- ----------------------------------------------------------------------------
-- 表: subscription_policy_links — 订阅所有权 → 它装进 quota_policies 的策略行
-- 到期/取消时据此关闭对应 quota 策略 (disable, 不删, 保审计历史)。
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS subscription_policy_links (
    id                    bigserial   PRIMARY KEY,
    tenant_id             bigint      NOT NULL REFERENCES tenants(id),
    user_subscription_id  bigint      NOT NULL,
    -- 指向 quota_policies.id (软引用, 不硬 FK 跨包耦合; tenant 一并存便于校验)
    quota_policy_id       bigint      NOT NULL,
    window_kind           text        NOT NULL,
    status                text        NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'closed')),
    created_at            timestamptz NOT NULL DEFAULT now(),
    closed_at             timestamptz,
    FOREIGN KEY (tenant_id, user_subscription_id) REFERENCES user_subscriptions (tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_subscription_policy_links_sub
    ON subscription_policy_links (tenant_id, user_subscription_id, status);
-- 一订阅一窗口一条 active 策略链接 (防重复装策略)
CREATE UNIQUE INDEX IF NOT EXISTS uq_subscription_policy_links_active
    ON subscription_policy_links (tenant_id, user_subscription_id, window_kind)
    WHERE status = 'active';

-- ----------------------------------------------------------------------------
-- 表: subscription_audit_events — 订阅操作审计轨迹 (信任链可审计)
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS subscription_audit_events (
    id                    bigserial   PRIMARY KEY,
    tenant_id             bigint      NOT NULL REFERENCES tenants(id),
    user_subscription_id  bigint      NOT NULL,
    event_type            text        NOT NULL
        CHECK (event_type IN (
            'subscription_created', 'expired', 'cancelled',
            'group_upgraded', 'group_downgraded', 'idempotent_replay'
        )),
    actor_kind            text        NOT NULL DEFAULT 'system'
        CHECK (actor_kind IN ('admin', 'user', 'system')),
    actor_id              bigint,
    reason_class          text,
    request_id            text,
    redacted_payload      jsonb,
    occurred_at           timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, user_subscription_id) REFERENCES user_subscriptions (tenant_id, id)
);
CREATE INDEX IF NOT EXISTS idx_subscription_audit_events_sub
    ON subscription_audit_events (tenant_id, user_subscription_id, occurred_at, id);

COMMIT;
