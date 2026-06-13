-- Hermes WAVE H4: mutating ops tools (the "fix" capability).
--
-- Background: the admin-gated Hermes ops assistant gains four MUTATING tools
-- (dlq_replay, account_pause, account_resume, renew_trigger) that wrap EXISTING
-- gateway mutation functions behind a 5-layer safety contract (RBAC, dry-run +
-- confirm, atomic audit, advisory lock, idempotency). Each invocation — preview
-- (dry-run), real execution, error, OR denial — is recorded in hermes_tool_calls
-- and mirrored into admin_audit_events as hermes.tool.<name>.
--
-- This migration is purely ADDITIVE + reversible:
--   1. extend hermes_tool_calls.tool_name CHECK (DROP+ADD) to admit the 4 new
--      mutating tool names alongside the H3 read-only six;
--   2. add hermes_tool_calls.dry_run (nullable boolean, default false) so a
--      dry-run preview row is distinguishable from a real execution row;
--   3. extend admin_audit_events.action CHECK (DROP+ADD) to admit the 4 new
--      hermes.tool.<name> mutating actions + the dlq_event target_type usage.
--
-- The .down reverts all three: drops the dry_run column and restores both CHECK
-- constraints to their pre-0146 state (the 0145 / pre-existing sets).

BEGIN;

-- (1) tool_name CHECK: admit the four mutating tool names.
ALTER TABLE hermes_tool_calls
    DROP CONSTRAINT IF EXISTS hermes_tool_calls_tool_name_check,
    ADD CONSTRAINT hermes_tool_calls_tool_name_check
        CHECK (tool_name IN (
            'credential_diagnose',
            'account_health_diagnose',
            'request_diagnose',
            'dlq_inspect',
            'audit_lookup',
            'log_analyze',
            'dlq_replay',
            'account_pause',
            'account_resume',
            'renew_trigger'));

-- (2) dry_run: mark a row as a read-only preview (true) vs a real execution
-- (false/null). Nullable + default false so existing read-only-tool rows (which
-- never preview) and existing data are unaffected.
ALTER TABLE hermes_tool_calls
    ADD COLUMN IF NOT EXISTS dry_run BOOLEAN NOT NULL DEFAULT false;

-- (3) admin_audit_events.action whitelist: admit the four hermes.tool.<name>
-- mutating actions. Mirrors the DROP+ADD CHECK shape of the prior admin-audit
-- action migrations (latest authoritative set defined in 0142). The full IN-list
-- is reproduced verbatim from 0142 (CHECK constraints are not composable),
-- preserving EVERY previously-allowed action and adding only the four new ones.
-- target_type is intentionally NOT constrained (no target_type CHECK exists), so
-- the dlq_event / provider_account / account_credential target_types the
-- mutating tools write need no whitelist change.
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
             'update_platform_settings',
             'unlock_user', 'force_disable_2fa', 'reset_passkey', 'set_user_group', 'set_user_remark',
             'set_user_status', 'create_user', 'delete_user',
             'create_quota_policy', 'update_quota_policy', 'delete_quota_policy',
             'clear_provider_account_rate_limit', 'update_provider_account',
             'hermes.tool.dlq_replay', 'hermes.tool.account_pause',
             'hermes.tool.account_resume', 'hermes.tool.renew_trigger'));

-- (4) admin_audit_events.target_type whitelist: admit 'dlq_event' (the target
-- type the dlq_replay mutating tool records). provider_account + account_credential
-- (the pause/resume + renew_trigger target types) are already whitelisted by 0139,
-- so only dlq_event is added. Without this, the dlq_replay admin-audit insert would
-- violate the target_type CHECK and abort the replay inside the orchestrator tx.
-- Full IN-list reproduced verbatim from 0139 (CHECK constraints are not composable).
ALTER TABLE admin_audit_events
    DROP CONSTRAINT IF EXISTS admin_audit_events_target_type_check,
    ADD CONSTRAINT admin_audit_events_target_type_check
        CHECK (target_type IN
            ('api_key', 'admin_token', 'tenant', 'user',
             'provider_account', 'account_credential',
             'billing_setting', 'pool_group', 'platform_setting',
             'quota_policy', 'dlq_event'));

COMMIT;
