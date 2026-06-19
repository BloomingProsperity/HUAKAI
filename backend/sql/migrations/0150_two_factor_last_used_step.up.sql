BEGIN;

-- 防重放:记录每个用户最近一次成功消费的 TOTP 时间步计数器(counter = unix / step)。
-- 加法列、可空(NULL 表示从未消费过任何时间步)。校验成功时只接受严格大于该值的时间步,
-- 据此拒绝同一个(或更早的)6 位码在其有效窗口内被重复使用(RFC 6238 §5.2)。
ALTER TABLE two_factor_settings
    ADD COLUMN IF NOT EXISTS last_used_step bigint;

COMMENT ON COLUMN two_factor_settings.last_used_step IS
    '最近一次成功消费的 TOTP 时间步计数器(counter = unix/step)。防重放守卫:仅接受匹配到的时间步严格大于该值的码;NULL 表示从未消费。';

COMMIT;
