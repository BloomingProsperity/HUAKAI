-- 回滚 HUAKAI 订阅子系统 P3a schema。
-- fail-closed: 若存在订阅实例数据则拒绝回滚 (不静默丢用户实时权益)。
-- 仅在无 user_subscriptions 数据时安全 (dev/test round-trip)。

BEGIN;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM user_subscriptions LIMIT 1) THEN
        RAISE EXCEPTION '0073 down 拒绝执行: user_subscriptions 存在实时订阅数据, 回滚会丢用户权益; 请先人工归档';
    END IF;
END $$;

DROP TABLE IF EXISTS subscription_audit_events;
DROP TABLE IF EXISTS subscription_policy_links;
DROP TABLE IF EXISTS user_subscriptions;
DROP TABLE IF EXISTS subscription_plans;

-- users.user_group 在 down 中移除 (无 user_subscriptions 数据时其值均为初始 'default' 或手工设置;
-- 上面的 fail-fast 已保证无订阅驱动的分组数据残留)。
ALTER TABLE users DROP COLUMN IF EXISTS user_group;

COMMIT;
