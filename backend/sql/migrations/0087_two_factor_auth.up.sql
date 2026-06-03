BEGIN;

CREATE TABLE IF NOT EXISTS two_factor_settings (
    tenant_id        bigint      NOT NULL REFERENCES tenants(id),
    user_id          bigint      NOT NULL,
    secret_enc       bytea       NOT NULL,
    is_enabled       boolean     NOT NULL DEFAULT false,
    failed_attempts  integer     NOT NULL DEFAULT 0 CHECK (failed_attempts >= 0),
    locked_until     timestamptz,
    last_used_at     timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id),
    UNIQUE (user_id),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS two_factor_backup_codes (
    id          bigserial   PRIMARY KEY,
    tenant_id   bigint      NOT NULL REFERENCES tenants(id),
    user_id     bigint      NOT NULL,
    code_hash   bytea       NOT NULL,
    is_used     boolean     NOT NULL DEFAULT false,
    used_at     timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id) ON DELETE CASCADE,
    UNIQUE (tenant_id, user_id, code_hash)
);

CREATE INDEX IF NOT EXISTS idx_two_factor_backup_codes_unused
    ON two_factor_backup_codes (tenant_id, user_id, created_at)
    WHERE is_used = false;

COMMENT ON TABLE two_factor_settings IS
    'Per-user TOTP two-factor settings. secret_enc stores a HUAKAI encrypted envelope, never plaintext.';

COMMENT ON TABLE two_factor_backup_codes IS
    'Per-user one-time 2FA backup codes. code_hash stores only a tenant/user bound hash.';

COMMIT;
