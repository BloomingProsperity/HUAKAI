-- 0051 down: 回滚到 0019 的 12 值白名单。 注意: 如果 production 已经插入了新
-- credential_state_* 事件, ADD CONSTRAINT CHECK 会验证既有行并失败。
-- operator 应先另起 DELETE 或迁移已落的新事件类型, 再执行该 down。
BEGIN;
ALTER TABLE credential_audit_events
    DROP CONSTRAINT IF EXISTS credential_audit_events_event_type_check,
    ADD CONSTRAINT credential_audit_events_event_type_check
        CHECK (event_type IN
            ('credential_created', 'credential_rotated', 'credential_disabled',
             'credential_deleted', 'credential_resolved', 'credential_refresh_succeeded',
             'credential_refresh_failed',
             'credential_acquisition_started', 'credential_acquisition_completed',
             'credential_acquisition_failed', 'credential_acquisition_cancelled',
             'gemini_cross_client_fallback'));
COMMIT;
