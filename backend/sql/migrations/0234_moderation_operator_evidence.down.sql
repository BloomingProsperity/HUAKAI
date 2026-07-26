BEGIN;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM moderation_violation_events)
       OR EXISTS (SELECT 1 FROM moderation_key_operations) THEN
        RAISE EXCEPTION 'refusing to roll back 0234: durable moderation facts exist';
    END IF;
END $$;

DROP TABLE IF EXISTS moderation_key_operations;
DROP TABLE IF EXISTS moderation_key_states;

-- 旧版本没有管理员处置决策枚举；降级时保留日志行和原因，把动作归入旧版
-- 可承载的后台拦截类别，避免重建旧约束时整笔回滚失败。
UPDATE moderation_log
SET decision = 'block_backend'
WHERE decision IN ('admin_disable', 'admin_unban');

ALTER TABLE moderation_log
    DROP CONSTRAINT IF EXISTS moderation_log_violation_event_fk,
    DROP CONSTRAINT IF EXISTS moderation_log_input_excerpt_length_check,
    DROP COLUMN IF EXISTS violation_event_id,
    DROP COLUMN IF EXISTS input_excerpt,
    DROP COLUMN IF EXISTS violation_count,
    DROP COLUMN IF EXISTS threshold_reached,
    DROP COLUMN IF EXISTS key_disabled,
    DROP COLUMN IF EXISTS actor_id,
    DROP COLUMN IF EXISTS actor_role,
    ADD COLUMN payload_hash text NOT NULL DEFAULT 'legacy_redacted';

ALTER TABLE moderation_log
    DROP CONSTRAINT IF EXISTS moderation_log_decision_check;

ALTER TABLE moderation_log
    ADD CONSTRAINT moderation_log_decision_check CHECK (
        decision IN (
            'pass',
            'block_keyword',
            'block_hash',
            'block_external',
            'block_backend',
            'fee_charged'
        )
    );

DROP INDEX IF EXISTS uq_moderation_violation_request;
DROP INDEX IF EXISTS uq_moderation_violation_tenant_id;

ALTER TABLE moderation_violation_events
    DROP CONSTRAINT IF EXISTS moderation_violation_api_key_fk,
    DROP CONSTRAINT IF EXISTS moderation_violation_user_fk,
    DROP COLUMN IF EXISTS ban_threshold_snapshot,
    DROP COLUMN IF EXISTS ban_window_seconds_snapshot,
    DROP COLUMN IF EXISTS violation_count,
    DROP COLUMN IF EXISTS threshold_reached,
    DROP COLUMN IF EXISTS auto_disable_enabled,
    DROP COLUMN IF EXISTS disposition_source,
    DROP COLUMN IF EXISTS disposition_result,
    ADD COLUMN payload_hash text NOT NULL DEFAULT 'legacy_redacted',
    ALTER COLUMN request_id DROP NOT NULL;

ALTER TABLE moderation_violation_events
    DROP CONSTRAINT IF EXISTS moderation_violation_events_decision_check;

ALTER TABLE moderation_violation_events
    ADD CONSTRAINT moderation_violation_events_decision_check CHECK (
        decision IN (
            'block_keyword',
            'block_hash',
            'block_external',
            'block_backend'
        )
    );

ALTER TABLE moderation_config
    DROP COLUMN IF EXISTS auto_disable_key_on_ban;

DROP TRIGGER IF EXISTS trg_api_keys_status_generation ON api_keys;
DROP FUNCTION IF EXISTS bump_api_key_status_generation();

ALTER TABLE api_keys
    DROP COLUMN IF EXISTS status_generation;

COMMIT;
