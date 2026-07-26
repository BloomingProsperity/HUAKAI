BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM usage_record_dlq
        WHERE last_manual_replay_at IS NOT NULL
           OR last_manual_replay_actor IS NOT NULL
    ) OR EXISTS (
        SELECT 1
        FROM dlq_events
        WHERE last_replay_at IS NOT NULL
           OR last_replay_actor IS NOT NULL
    ) THEN
        RAISE EXCEPTION
            '不能回滚 0221：仍存在人工重放操作者日志，请先显式迁移这些运营事实';
    END IF;
END
$$;

ALTER TABLE usage_record_dlq
    DROP COLUMN IF EXISTS last_manual_replay_at,
    DROP COLUMN IF EXISTS last_manual_replay_actor;

ALTER TABLE dlq_events
    DROP COLUMN IF EXISTS last_replay_at,
    DROP COLUMN IF EXISTS last_replay_actor;

COMMIT;
