-- 0070_subscription_domain.down.sql
--
-- 回滚 SUBSCRIPTION-1 additive tables。

BEGIN;

DROP TABLE IF EXISTS user_subscriptions;
DROP TABLE IF EXISTS subscription_orders;
DROP TABLE IF EXISTS subscription_plans;

COMMIT;
