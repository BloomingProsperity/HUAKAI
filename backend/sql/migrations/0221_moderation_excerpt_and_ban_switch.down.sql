-- 0221_moderation_excerpt_and_ban_switch.down.sql
--
-- 回退加列。回退后旧代码路径不依赖这三列：摘录只是展示用附加事实，
-- 停用开关缺失时回到「达阈值即停用」的既有默认行为。

BEGIN;

ALTER TABLE moderation_config
    DROP COLUMN IF EXISTS auto_disable_key_on_ban;

ALTER TABLE moderation_log
    DROP COLUMN IF EXISTS input_excerpt;

ALTER TABLE moderation_violation_events
    DROP COLUMN IF EXISTS input_excerpt;

COMMENT ON TABLE moderation_violation_events IS
    'Non-sampled content moderation block events used only for auto-ban windows; stores metadata and payload hashes, never raw bodies or credentials.';

COMMIT;
