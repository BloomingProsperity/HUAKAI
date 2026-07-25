-- 0221_moderation_excerpt_and_ban_switch.up.sql
--
-- 两件事：
--
-- 1) 违规内容摘录。此前违规事件与审核日志只存 payload_hash，管理员看到的只有
--    「命中了哪条规则」，无法判断这次请求到底在做什么，也就无法决定该不该处置。
--    新增 input_excerpt 保存**已脱敏并截断**的用户消息片段：写入前先按凭证模式
--    脱敏、再按字符（rune）截断，因此该列不承载完整原始请求体，也不承载密钥、
--    token、cookie 等凭据。
--
-- 2) 达阈值后的停用改为可控。此前窗口内违规累计到阈值就直接停用 API Key，
--    没有人工介入余地。新增 auto_disable_key_on_ban 开关：关闭时达阈值只落库
--    （违规事件、计数、达标事实全部保留），由管理员看过摘录后人工处置；
--    开启时维持既有自动停用行为。
--
--    该开关必须独立于 ban_threshold：把阈值设为 0 会让计数链在记录违规之前
--    提前返回，连违规事件与计数一起丢失，无法充当「只记录不停用」的开关。
--
-- 默认值：auto_disable_key_on_ban 默认 false，即默认需要人工确认后才停用。

BEGIN;

ALTER TABLE moderation_violation_events
    ADD COLUMN IF NOT EXISTS input_excerpt text NOT NULL DEFAULT '';

ALTER TABLE moderation_log
    ADD COLUMN IF NOT EXISTS input_excerpt text NOT NULL DEFAULT '';

ALTER TABLE moderation_config
    ADD COLUMN IF NOT EXISTS auto_disable_key_on_ban boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN moderation_violation_events.input_excerpt IS
    'Redacted, rune-truncated excerpt of the user message that triggered the block; never raw request bodies or credentials.';

COMMENT ON COLUMN moderation_log.input_excerpt IS
    'Redacted, rune-truncated excerpt of the user message for this decision; never raw request bodies or credentials.';

COMMENT ON COLUMN moderation_config.auto_disable_key_on_ban IS
    'When false (default), reaching the violation threshold records the event without disabling the API key, leaving disposition to an operator.';

COMMENT ON TABLE moderation_violation_events IS
    'Non-sampled content moderation block events used for auto-ban windows and operator review; stores metadata, payload hashes and redacted excerpts, never raw bodies or credentials.';

COMMIT;
