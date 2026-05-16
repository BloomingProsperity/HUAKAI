-- 0019_credential_acquisition_flow_sessions.down.sql
--
-- Rollback F-CRED-001 acquisition flow sessions and restore audit CHECK
-- constraints to the pre-0019 credential/admin event sets.

BEGIN;

UPDATE credential_audit_events
SET event_type = 'credential_refresh_failed',
    payload = COALESCE(payload, '{}'::jsonb) || jsonb_build_object('archived_event_type', event_type)
WHERE event_type LIKE 'credential_acquisition_%'
   OR event_type = 'gemini_cross_client_fallback';

DROP TABLE IF EXISTS credential_acquisition_flow_sessions;

ALTER TABLE credential_audit_events
    DROP CONSTRAINT IF EXISTS credential_audit_events_event_type_check,
    ADD CONSTRAINT credential_audit_events_event_type_check
        CHECK (event_type IN
            ('credential_created', 'credential_rotated', 'credential_disabled',
             'credential_deleted', 'credential_resolved', 'credential_refresh_succeeded',
             'credential_refresh_failed'));

ALTER TABLE admin_audit_events
    DROP CONSTRAINT IF EXISTS admin_audit_events_action_check,
    ADD CONSTRAINT admin_audit_events_action_check
        CHECK (action IN
            ('issue_api_key', 'revoke_api_key', 'list_api_keys',
             'issue_admin_token', 'revoke_admin_token', 'admin_login',
             'create_provider_account', 'disable_provider_account',
             'enable_provider_account', 'delete_provider_account',
             'create_account_credential', 'rotate_account_credential',
             'disable_account_credential', 'delete_account_credential',
             'list_account_credentials'));

COMMIT;
