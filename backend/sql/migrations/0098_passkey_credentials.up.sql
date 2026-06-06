BEGIN;

CREATE TABLE IF NOT EXISTS passkey_credentials (
    id               BIGSERIAL PRIMARY KEY,
    tenant_id        BIGINT      NOT NULL REFERENCES tenants(id),
    user_id          BIGINT      NOT NULL,
    credential_id    BYTEA       NOT NULL,
    public_key       BYTEA       NOT NULL,
    sign_count       BIGINT      NOT NULL DEFAULT 0 CHECK (sign_count >= 0),
    aaguid           BYTEA,
    attestation_type TEXT,
    transports       TEXT,
    clone_warning    BOOLEAN    NOT NULL DEFAULT false,
    name             TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at     TIMESTAMPTZ,
    UNIQUE (tenant_id, credential_id),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_passkey_credentials_tenant_user
    ON passkey_credentials (tenant_id, user_id);

CREATE TABLE IF NOT EXISTS webauthn_session (
    id           TEXT        PRIMARY KEY,
    tenant_id    BIGINT      NOT NULL REFERENCES tenants(id),
    user_id      BIGINT,
    purpose      TEXT        NOT NULL CHECK (purpose IN ('register', 'login')),
    session_data JSONB       NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_webauthn_session_expiry
    ON webauthn_session (expires_at);

CREATE INDEX IF NOT EXISTS idx_webauthn_session_tenant_purpose
    ON webauthn_session (tenant_id, purpose, expires_at);

COMMIT;
