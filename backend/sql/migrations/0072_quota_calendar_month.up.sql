-- quota 引擎扩展: window_kind 增加 'calendar_month' (日历月窗口)。
-- 订阅子系统 (0073) 的 monthly_cap_usd 会装成 calendar_month 的 cost_usd 策略;
-- 引擎 ComputeWindow 已支持月窗口边界 (当月 1 号 -> 下月 1 号 UTC)。
-- 本 ALTER 仅放宽 CHECK 取值集合, 对既有行无影响 (additive)。

BEGIN;

ALTER TABLE quota_policies
    DROP CONSTRAINT IF EXISTS quota_policies_window_kind_check,
    ADD CONSTRAINT quota_policies_window_kind_check
        CHECK (window_kind IN (
            'none', 'fixed', 'calendar_day',
            'calendar_week', 'calendar_month', 'manual'
        ));

COMMIT;
