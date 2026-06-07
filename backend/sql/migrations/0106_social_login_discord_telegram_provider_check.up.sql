-- 0106_social_login_discord_telegram_provider_check.up.sql
--
-- Additive provider CHECK widening for Module A social login closure.

BEGIN;

ALTER TABLE social_identity_links
    DROP CONSTRAINT IF EXISTS social_identity_links_provider_check;
ALTER TABLE social_identity_links
    ADD CONSTRAINT social_identity_links_provider_check
    CHECK (provider IN ('google', 'github', 'wechat', 'dingtalk', 'linuxdo', 'oidc', 'discord', 'telegram'));

ALTER TABLE oauth_flow_sessions
    DROP CONSTRAINT IF EXISTS oauth_flow_sessions_provider_check;
ALTER TABLE oauth_flow_sessions
    ADD CONSTRAINT oauth_flow_sessions_provider_check
    CHECK (provider IN ('google', 'github', 'wechat', 'dingtalk', 'linuxdo', 'oidc', 'discord', 'telegram'));

COMMIT;
