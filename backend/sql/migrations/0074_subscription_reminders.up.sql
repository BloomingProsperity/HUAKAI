-- HUAKAI 订阅到期提醒投递账本 (Slice P3b-1)。
-- 目的: 给到期前分档提醒邮件做持久去重 —— 同一订阅同一档位只发一次, worker 多次 tick /
--   重启都不重发。提醒发送复用 internal/email (SMTP + DLQ 重试), 本表只记"是否已就该档位发过"。
-- 模型: 一行 = 某订阅某档位 (到期前天数) 的一次提醒结果。
--   reminder_key = 到期前天数字符串 ('7'/'3'/'1' 等, 由 worker 的 offset 列表决定)。
--   去重靠 (tenant_id, user_subscription_id, reminder_key) 唯一索引。
-- 不碰钱表 (billing_events/payment_credits); 纯提醒投递记录。

BEGIN;

CREATE TABLE IF NOT EXISTS subscription_expiry_reminders (
    id                    bigserial   PRIMARY KEY,
    tenant_id             bigint      NOT NULL REFERENCES tenants(id),
    user_subscription_id  bigint      NOT NULL,
    -- 档位标识: 到期前天数 (worker offset), 如 '7'/'3'/'1'
    reminder_key          text        NOT NULL,
    -- 投递结果 (仅终态记账): sent=已投递或已入 DLQ 待重试; skipped_no_recipient=用户无邮箱跳过。
    --   发送失败一律不记账 (当可重试, 见 reminder.go ReminderRetry), 故无 permanent_failed 终态。
    status                text        NOT NULL DEFAULT 'sent'
        CHECK (status IN ('sent', 'skipped_no_recipient')),
    -- 发往的收件邮箱 (用户自己的; 排错/审计用; 跳过无收件人时为空串)
    recipient             text        NOT NULL DEFAULT '',
    -- 记录时订阅的到期时间快照 (排错用: 确认提醒确实对应该到期日)
    expires_at_snapshot   timestamptz NOT NULL,
    created_at            timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, user_subscription_id) REFERENCES user_subscriptions (tenant_id, id)
);

-- 去重核心: 同订阅同档位仅一条 (任意 status)。重复 tick 命中冲突即跳过。
CREATE UNIQUE INDEX IF NOT EXISTS uq_subscription_expiry_reminders
    ON subscription_expiry_reminders (tenant_id, user_subscription_id, reminder_key);

-- 按订阅查已发档位 (worker 跳过判定)。
CREATE INDEX IF NOT EXISTS idx_subscription_expiry_reminders_sub
    ON subscription_expiry_reminders (tenant_id, user_subscription_id);

-- 提醒 worker 的扫描是跨租户、按 expires_at 升序 (非 tenant-leading), 现有
-- idx_user_subscriptions_due_expiry (tenant_id, expires_at) 不匹配该全局范围/排序模式。
-- 加一个 tenant 无关、匹配 (expires_at, id) 游标翻页的偏索引, 防活跃订阅表增长后退化为全表扫。
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_active_expiry_global
    ON user_subscriptions (expires_at, id)
    WHERE status = 'active';

COMMIT;
