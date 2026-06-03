-- 0077_platform_settings.up.sql
--
-- Consolidated platform-wide runtime settings for admin-managed toggles.
-- scope='global' is the only supported scope in this first slice.
-- Credential material is never stored here; OAuth/CAPTCHA secrets remain in
-- credential storage. This table stores only the explicit non-secret allow-list
-- accepted by internal/platformsettings.

BEGIN;

CREATE TABLE IF NOT EXISTS platform_settings (
    id            bigserial   PRIMARY KEY,
    scope         text        NOT NULL DEFAULT 'global',
    setting_key   text        NOT NULL,
    setting_value text        NOT NULL,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    updated_by    text        NOT NULL,
    UNIQUE (scope, setting_key),
    CHECK (scope <> ''),
    CHECK (setting_key <> ''),
    CHECK (setting_value <> '')
);

CREATE INDEX IF NOT EXISTS idx_platform_settings_scope_key
    ON platform_settings (scope, setting_key);

COMMENT ON TABLE platform_settings IS
    'Platform-wide runtime-mutable admin settings. scope=global for v1. No credential material is stored here.';

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
             'list_account_credentials',
             'credential_acquisition_started', 'credential_acquisition_completed',
             'credential_acquisition_failed', 'credential_acquisition_cancelled',
             'update_billing_settings',
             'create_pool_group', 'update_pool_group',
             'update_platform_settings'));

ALTER TABLE admin_audit_events
    DROP CONSTRAINT IF EXISTS admin_audit_events_target_type_check,
    ADD CONSTRAINT admin_audit_events_target_type_check
        CHECK (target_type IN
            ('api_key', 'admin_token', 'tenant', 'user',
             'provider_account', 'account_credential',
             'billing_setting', 'pool_group', 'platform_setting'));

COMMIT;
