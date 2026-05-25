-- 0014_provider_account_admin_action.down.sql
--
-- 回滚 provider_account admin action / target_type 白名单扩展。
-- 若已存在新 action 审计行，PostgreSQL 会拒绝回滚；这是刻意保守行为。

BEGIN;

ALTER TABLE admin_audit_events
    DROP CONSTRAINT IF EXISTS admin_audit_events_action_check,
    ADD CONSTRAINT admin_audit_events_action_check
        CHECK (action IN
            ('issue_api_key', 'revoke_api_key', 'list_api_keys',
             'issue_admin_token', 'revoke_admin_token',
             'admin_login'));

ALTER TABLE admin_audit_events
    DROP CONSTRAINT IF EXISTS admin_audit_events_target_type_check,
    ADD CONSTRAINT admin_audit_events_target_type_check
        CHECK (target_type IN
            ('api_key', 'admin_token', 'tenant', 'user'));

COMMIT;
