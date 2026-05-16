-- 0020_user_authentication.up.sql
--
-- F-AUTH-007: HUAKAI platform user authentication.
-- Additive over the L0 `users` table from 0007. This keeps existing
-- api_keys -> users bindings intact while adding local login state.

BEGIN;

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS password_hash text,
    ADD COLUMN IF NOT EXISTS email_verified boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS invite_code_used text,
    ADD COLUMN IF NOT EXISTS social_login_provider text,
    ADD COLUMN IF NOT EXISTS password_version integer NOT NULL DEFAULT 1 CHECK (password_version >= 1),
    ADD COLUMN IF NOT EXISTS failed_login_count integer NOT NULL DEFAULT 0 CHECK (failed_login_count >= 0),
    ADD COLUMN IF NOT EXISTS locked_until timestamptz;

ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_status_check,
    ADD CONSTRAINT users_status_check CHECK (status IN (
        'pending_verification',
        'active',
        'disabled',
        'locked',
        'reset_required',
        'deleted'
    ));

CREATE TABLE IF NOT EXISTS email_verification_tokens (
    id              uuid        PRIMARY KEY,
    tenant_id       bigint      NOT NULL REFERENCES tenants(id),
    user_id         bigint      NOT NULL,
    token_hash      bytea       NOT NULL,
    expires_at      timestamptz NOT NULL,
    consumed_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_email_verification_token_hash
    ON email_verification_tokens (token_hash);

CREATE INDEX IF NOT EXISTS idx_email_verification_user_active
    ON email_verification_tokens (tenant_id, user_id, expires_at)
    WHERE consumed_at IS NULL;

CREATE TABLE IF NOT EXISTS password_reset_tokens (
    id                  uuid        PRIMARY KEY,
    tenant_id           bigint      NOT NULL REFERENCES tenants(id),
    user_id             bigint      NOT NULL,
    token_hash          bytea       NOT NULL,
    password_version    integer     NOT NULL,
    expires_at          timestamptz NOT NULL,
    consumed_at         timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_password_reset_token_hash
    ON password_reset_tokens (token_hash);

CREATE INDEX IF NOT EXISTS idx_password_reset_user_active
    ON password_reset_tokens (tenant_id, user_id, expires_at)
    WHERE consumed_at IS NULL;

CREATE TABLE IF NOT EXISTS invite_codes (
    code            text        PRIMARY KEY,
    tenant_id       bigint      NOT NULL REFERENCES tenants(id),
    created_by      bigint,
    max_uses        integer     NOT NULL DEFAULT 1 CHECK (max_uses > 0),
    used_count      integer     NOT NULL DEFAULT 0 CHECK (used_count >= 0),
    valid_until     timestamptz,
    status          text        NOT NULL DEFAULT 'active'
                                CHECK (status IN ('active', 'disabled', 'expired', 'exhausted')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, created_by) REFERENCES users (tenant_id, id),
    CONSTRAINT invite_codes_used_count_max CHECK (used_count <= max_uses)
);

CREATE INDEX IF NOT EXISTS idx_invite_codes_tenant_status
    ON invite_codes (tenant_id, status, valid_until);

CREATE TABLE IF NOT EXISTS invite_bindings (
    id              uuid        PRIMARY KEY,
    tenant_id       bigint      NOT NULL REFERENCES tenants(id),
    user_id         bigint      NOT NULL,
    invite_code     text        NOT NULL,
    redeemed_at     timestamptz NOT NULL DEFAULT now(),
    created_at      timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id),
    FOREIGN KEY (invite_code) REFERENCES invite_codes (code),
    UNIQUE (tenant_id, user_id, invite_code)
);

CREATE INDEX IF NOT EXISTS idx_invite_bindings_user
    ON invite_bindings (tenant_id, user_id, redeemed_at DESC);

CREATE TABLE IF NOT EXISTS social_identity_links (
    tenant_id       bigint      NOT NULL REFERENCES tenants(id),
    user_id         bigint      NOT NULL,
    provider        text        NOT NULL CHECK (provider IN ('google', 'github')),
    subject         text        NOT NULL,
    email_verified  boolean     NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, provider, subject),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)
);

CREATE INDEX IF NOT EXISTS idx_social_identity_links_user
    ON social_identity_links (tenant_id, user_id, provider);

CREATE TABLE IF NOT EXISTS oauth_flow_sessions (
    id              uuid        PRIMARY KEY,
    tenant_id       bigint      NOT NULL REFERENCES tenants(id),
    provider        text        NOT NULL CHECK (provider IN ('google', 'github')),
    state_hash      bytea       NOT NULL,
    nonce_hash      bytea       NOT NULL,
    pkce_verifier   text        NOT NULL,
    redirect_uri    text,
    expires_at      timestamptz NOT NULL,
    consumed_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_oauth_flow_state_hash
    ON oauth_flow_sessions (state_hash);

CREATE INDEX IF NOT EXISTS idx_oauth_flow_active
    ON oauth_flow_sessions (tenant_id, provider, expires_at)
    WHERE consumed_at IS NULL;

COMMENT ON COLUMN users.password_hash IS
    'F-AUTH-007 argon2id password hash. Raw password is never stored.';
COMMENT ON COLUMN users.invite_code_used IS
    'SHA-256/base64url hash of redeemed invite code; raw invite code is never stored.';
COMMENT ON TABLE email_verification_tokens IS
    'F-AUTH-007 one-time email verification challenges. token_hash only; raw token is never stored.';
COMMENT ON TABLE password_reset_tokens IS
    'F-AUTH-007 one-time password reset challenges. token_hash only; raw token is never stored.';
COMMENT ON TABLE invite_codes IS
    'F-AUTH-007 invite grants. code stores the hashed invite code, not the raw invite.';
COMMENT ON TABLE invite_bindings IS
    'F-AUTH-007 atomic invite redemption binding to the created User.';
COMMENT ON TABLE social_identity_links IS
    'F-AUTH-007 verified social identity subjects. No upstream OAuth token material is stored.';
COMMENT ON TABLE oauth_flow_sessions IS
    'F-AUTH-007 short-lived OAuth state/nonce/PKCE flow sessions. OAuth access tokens are never stored here.';

COMMIT;
