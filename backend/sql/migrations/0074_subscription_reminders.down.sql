-- 回滚 0074: 删除订阅到期提醒投递账本。
-- 账本仅做去重历史, 无下游数据依赖; 直接删表。

BEGIN;

DROP INDEX IF EXISTS idx_user_subscriptions_active_expiry_global;
DROP TABLE IF EXISTS subscription_expiry_reminders;

COMMIT;
