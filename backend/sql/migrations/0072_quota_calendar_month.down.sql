-- 回滚 calendar_month 窗口支持。
-- fail-closed: 若已存在 calendar_month 策略则拒绝回滚 (否则 CHECK 收窄会失败/丢策略)。

BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM quota_policies WHERE window_kind = 'calendar_month' LIMIT 1
    ) THEN
        RAISE EXCEPTION '0072 down 拒绝执行: 存在 window_kind=calendar_month 的 quota_policies, 回滚会破坏数据; 请先迁移这些策略';
    END IF;
END $$;

ALTER TABLE quota_policies
    DROP CONSTRAINT IF EXISTS quota_policies_window_kind_check,
    ADD CONSTRAINT quota_policies_window_kind_check
        CHECK (window_kind IN (
            'none', 'fixed', 'calendar_day',
            'calendar_week', 'manual'
        ));

COMMIT;
