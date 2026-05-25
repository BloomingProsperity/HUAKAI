-- 0021_session_management.up.sql
--
-- F-SESSION-001: HUAKAI platform user session families and rotating
-- refresh-token state. These records are independent from upstream Provider
-- Account credentials and sticky pool affinity.

BEGIN;

CREATE TABLE IF NOT EXISTS session_families (
    id                  uuid        PRIMARY KEY,
    user_id             bigint      NOT NULL,
    tenant_id           bigint      NOT NULL REFERENCES tenants(id),
    status              text        NOT NULL DEFAULT 'active'
                                    CHECK (status IN ('active', 'revoked', 'expired', 'suspicious', 'replaced')),
    generation          integer     NOT NULL DEFAULT 1 CHECK (generation >= 1),
    created_at          timestamptz NOT NULL DEFAULT now(),
    last_active_at      timestamptz NOT NULL DEFAULT now(),
    device_info         jsonb       NOT NULL DEFAULT '{}'::jsonb,
    ip_baseline         text        NOT NULL DEFAULT '',
    revoked_at          timestamptz,
    revoked_reason      text,
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id),
    CONSTRAINT session_families_device_info_object CHECK (jsonb_typeof(device_info) = 'object')
);

CREATE INDEX IF NOT EXISTS idx_session_families_user_status
    ON session_families (tenant_id, user_id, status, last_active_at DESC);

CREATE INDEX IF NOT EXISTS idx_session_families_last_active
    ON session_families (status, last_active_at)
    WHERE status IN ('active', 'suspicious');

CREATE TABLE IF NOT EXISTS session_tokens (
    id              uuid        PRIMARY KEY,
    tenant_id       bigint      NOT NULL REFERENCES tenants(id),
    family_id       uuid        NOT NULL REFERENCES session_families(id) ON DELETE CASCADE,
    token_hash      bytea       NOT NULL,
    generation      integer     NOT NULL CHECK (generation >= 1),
    expires_at      timestamptz NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    last_used_at    timestamptz,
    revoked_at      timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_session_tokens_hash
    ON session_tokens (token_hash);

CREATE INDEX IF NOT EXISTS idx_session_tokens_family_active
    ON session_tokens (tenant_id, family_id, generation DESC)
    WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_session_tokens_expiry
    ON session_tokens (expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS refresh_tokens (
    id              uuid        PRIMARY KEY,
    tenant_id       bigint      NOT NULL REFERENCES tenants(id),
    family_id       uuid        NOT NULL REFERENCES session_families(id) ON DELETE CASCADE,
    token_hash      bytea       NOT NULL,
    generation      integer     NOT NULL CHECK (generation >= 1),
    status          text        NOT NULL DEFAULT 'active'
                                CHECK (status IN ('active', 'consumed', 'revoked', 'expired')),
    expires_at      timestamptz NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    consumed_at     timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_refresh_tokens_hash
    ON refresh_tokens (token_hash);

CREATE UNIQUE INDEX IF NOT EXISTS uq_refresh_tokens_family_generation
    ON refresh_tokens (family_id, generation);

CREATE INDEX IF NOT EXISTS idx_refresh_tokens_family_status
    ON refresh_tokens (tenant_id, family_id, status, generation DESC);

CREATE INDEX IF NOT EXISTS idx_refresh_tokens_expiry
    ON refresh_tokens (status, expires_at)
    WHERE status = 'active';

COMMENT ON TABLE session_families IS
    'F-SESSION-001 platform login session family. Not upstream Provider Account credential state.';
COMMENT ON TABLE session_tokens IS
    'F-SESSION-001 short-lived signed platform session tokens. Stores token_hash only; raw token is never stored.';
COMMENT ON TABLE refresh_tokens IS
    'F-SESSION-001 rotating refresh tokens. Stores token_hash only; raw token is never stored.';

COMMIT;
