-- 回滚 0075: 逆序拆除订阅激活地基。
-- 注: 恢复 user_subscriptions.source / subscription_audit_events.event_type 原 CHECK 前,
--   若已有 'voucher' source 或 'subscription_renewed' 事件行, ADD CONSTRAINT 会失败 (fail-closed,
--   提示有依赖新值的数据, 不静默丢约束)。

BEGIN;

DROP TABLE IF EXISTS subscription_fulfillment_effects;

ALTER TABLE subscription_audit_events DROP CONSTRAINT IF EXISTS subscription_audit_events_event_type_check;
ALTER TABLE subscription_audit_events ADD CONSTRAINT subscription_audit_events_event_type_check
    CHECK (event_type IN (
        'subscription_created', 'expired', 'cancelled',
        'group_upgraded', 'group_downgraded', 'idempotent_replay'
    ));

ALTER TABLE user_subscriptions DROP CONSTRAINT IF EXISTS user_subscriptions_source_check;
ALTER TABLE user_subscriptions ADD CONSTRAINT user_subscriptions_source_check
    CHECK (source IN ('admin', 'order'));

ALTER TABLE voucher DROP CONSTRAINT IF EXISTS voucher_subscription_plan_fk;
ALTER TABLE voucher DROP CONSTRAINT IF EXISTS voucher_subscription_kind_check;
ALTER TABLE voucher DROP CONSTRAINT IF EXISTS voucher_grant_kind_check;
ALTER TABLE voucher DROP COLUMN IF EXISTS subscription_plan_id;
ALTER TABLE voucher DROP COLUMN IF EXISTS grant_kind;

DROP INDEX IF EXISTS idx_payment_orders_kind;
ALTER TABLE payment_orders DROP CONSTRAINT IF EXISTS payment_orders_subscription_plan_fk;
ALTER TABLE payment_orders DROP CONSTRAINT IF EXISTS payment_orders_subscription_kind_check;
ALTER TABLE payment_orders DROP CONSTRAINT IF EXISTS payment_orders_order_kind_check;
ALTER TABLE payment_orders DROP COLUMN IF EXISTS subscription_plan_id;
ALTER TABLE payment_orders DROP COLUMN IF EXISTS order_kind;

COMMIT;
