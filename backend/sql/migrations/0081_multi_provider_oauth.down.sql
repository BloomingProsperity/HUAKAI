-- 0081_multi_provider_oauth.down.sql
--
-- DESTRUCTIVE: drops tenant OIDC provider configuration and pending OAuth sessions.
-- Running this on production requires an owner-approved backup and rollback plan.

BEGIN;

DROP TABLE IF EXISTS oidc_provider_configs;
DROP TABLE IF EXISTS pending_oauth_sessions;

ALTER TABLE oauth_flow_sessions
    DROP CONSTRAINT IF EXISTS oauth_flow_sessions_provider_check;
ALTER TABLE oauth_flow_sessions
    ADD CONSTRAINT oauth_flow_sessions_provider_check
    CHECK (provider IN ('google', 'github'));

ALTER TABLE social_identity_links
    DROP CONSTRAINT IF EXISTS social_identity_links_provider_check;
ALTER TABLE social_identity_links
    ADD CONSTRAINT social_identity_links_provider_check
    CHECK (provider IN ('google', 'github'));

COMMIT;
