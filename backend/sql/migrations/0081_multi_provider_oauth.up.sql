-- 0081_multi_provider_oauth.up.sql
--
-- First multi-oauth slice: widen normalized provider constraints and add
-- additive state tables needed by later pending-email and generic OIDC waves.

BEGIN;

ALTER TABLE social_identity_links
    DROP CONSTRAINT IF EXISTS social_identity_links_provider_check;
ALTER TABLE social_identity_links
    ADD CONSTRAINT social_identity_links_provider_check
    CHECK (provider IN ('google', 'github', 'wechat', 'dingtalk', 'linuxdo', 'oidc'));

ALTER TABLE oauth_flow_sessions
    DROP CONSTRAINT IF EXISTS oauth_flow_sessions_provider_check;
ALTER TABLE oauth_flow_sessions
    ADD CONSTRAINT oauth_flow_sessions_provider_check
    CHECK (provider IN ('google', 'github', 'wechat', 'dingtalk', 'linuxdo', 'oidc'));

CREATE TABLE IF NOT EXISTS pending_oauth_sessions (
    id                      uuid        PRIMARY KEY,
    tenant_id               bigint      NOT NULL REFERENCES tenants(id),
    provider                text        NOT NULL
                                CHECK (provider IN ('wechat', 'dingtalk', 'linuxdo', 'oidc')),
    subject_ciphertext      bytea       NOT NULL,
    display_name_ciphertext bytea,
    token_hash              bytea       NOT NULL,
    expires_at              timestamptz NOT NULL,
    consumed_at             timestamptz,
    created_at              timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_pending_oauth_token_hash
    ON pending_oauth_sessions (token_hash);

CREATE INDEX IF NOT EXISTS idx_pending_oauth_active
    ON pending_oauth_sessions (tenant_id, provider, expires_at)
    WHERE consumed_at IS NULL;

CREATE TABLE IF NOT EXISTS oidc_provider_configs (
    id                   bigserial   PRIMARY KEY,
    tenant_id            bigint      NOT NULL REFERENCES tenants(id),
    slug                 text        NOT NULL,
    issuer               text        NOT NULL,
    discovery_url        text,
    client_id            text        NOT NULL,
    client_secret_cipher bytea       NOT NULL,
    scopes               text[]      NOT NULL DEFAULT ARRAY['openid', 'email', 'profile']::text[],
    claim_subject        text        NOT NULL DEFAULT 'sub',
    claim_email          text        NOT NULL DEFAULT 'email',
    claim_display_name   text        NOT NULL DEFAULT 'name',
    redirect_uri         text,
    enabled              boolean     NOT NULL DEFAULT true,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, slug)
);

CREATE INDEX IF NOT EXISTS idx_oidc_provider_configs_tenant_enabled
    ON oidc_provider_configs (tenant_id, enabled);

COMMENT ON TABLE pending_oauth_sessions IS
    'Short-lived two-step OAuth sessions for providers that cannot return a verified email. subject_ciphertext uses AES-256-GCM with AAD=(tenant_id||provider||id); raw subject is never stored.';

COMMENT ON TABLE oidc_provider_configs IS
    'Admin-configurable generic OIDC provider config per tenant. Runtime lookup must enforce tenant_id + slug + enabled=true; client_secret_cipher stores the encrypted secret envelope.';

COMMIT;
