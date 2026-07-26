BEGIN;

ALTER TABLE usage_record_dlq
    ADD COLUMN IF NOT EXISTS last_manual_replay_at timestamptz,
    ADD COLUMN IF NOT EXISTS last_manual_replay_actor text;

ALTER TABLE dlq_events
    ADD COLUMN IF NOT EXISTS last_replay_at timestamptz,
    ADD COLUMN IF NOT EXISTS last_replay_actor text;

COMMENT ON COLUMN usage_record_dlq.last_manual_replay_actor IS
    '执行最近一次人工重放的已认证管理员归属标识，不保存凭据。';
COMMENT ON COLUMN dlq_events.last_replay_actor IS
    '执行最近一次人工重放的已认证管理员归属标识，不保存凭据。';

COMMIT;
