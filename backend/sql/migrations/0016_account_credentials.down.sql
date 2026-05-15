-- F-AUTH-005 reverse of 0016_account_credentials.up.sql
-- Conservative rollback: if admin_audit_events still contains credential
-- actions or targets, PostgreSQL rejects the restored CHECK constraints.

BEGIN;

DROP INDEX IF EXISTS idx_credential_audit_event_time;
DROP INDEX IF EXISTS idx_credential_audit_credential_time;
DROP INDEX IF EXISTS idx_credential_audit_account_time;
DROP TABLE IF EXISTS credential_audit_events;

ALTER TABLE admin_audit_events
    DROP CONSTRAINT IF EXISTS admin_audit_events_action_check,
    ADD CONSTRAINT admin_audit_events_action_check
        CHECK (action IN
            ('issue_api_key', 'revoke_api_key', 'list_api_keys',
             'issue_admin_token', 'revoke_admin_token', 'admin_login',
             'create_provider_account', 'disable_provider_account',
             'enable_provider_account', 'delete_provider_account'));

ALTER TABLE admin_audit_events
    DROP CONSTRAINT IF EXISTS admin_audit_events_target_type_check,
    ADD CONSTRAINT admin_audit_events_target_type_check
        CHECK (target_type IN
            ('api_key', 'admin_token', 'tenant', 'user',
             'provider_account'));

DROP INDEX IF EXISTS idx_account_credentials_state;
DROP INDEX IF EXISTS idx_account_credentials_account;
DROP INDEX IF EXISTS idx_account_credentials_refresh;
DROP INDEX IF EXISTS uq_account_credentials_active_mode;
DROP TABLE IF EXISTS account_credentials;

COMMIT;
