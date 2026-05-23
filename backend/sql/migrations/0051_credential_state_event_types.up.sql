-- 0051 credential_state_event_types: 把 W5 C1 SetState audit 事件分类的 5 个新 event_type
-- 加进 credential_audit_events_event_type_check CHECK enum 白名单。
-- additive: 既有 12 个 enum 全保留, 不影响既有 audit 行;只允许更细的 state-transition
-- 事件 (activated / disabled / revoked / attention / changed) 落库。
-- 关联: backend/internal/credentialstore/audit_events.go event 常量。
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
             'gemini_cross_client_fallback',
             'credential_state_activated', 'credential_state_disabled',
             'credential_state_revoked', 'credential_state_attention',
             'credential_state_changed'));
COMMIT;
