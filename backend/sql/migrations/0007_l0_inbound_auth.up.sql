-- Phase L0 minimum (N+4a): table-backed inbound auth.
-- Replaces the SmokeAuthResolver env-injected single bearer pattern.
-- Per docs/process/plans/2026-04-30-n4-l0-minimum.md (synthesized) D1-D10.
--
-- Out of scope here (deferred to N+4b): adding FKs from
-- billing_ledger_claims / usage_records / pool_slot_acquisitions back to
-- api_keys(id) / users(id). Existing fixtures use synthetic ids
-- (apiKeyID = tenantID*100+1) and would break under those FKs.

BEGIN;

-- ----------------------------------------------------------------------------
-- Table: users
-- L0 minimum end-user identity. email is nullable because L0 does not
-- require self-signup; admin can create users with display_name only.
-- No password column: HUAKAI authenticates via api_keys, not user-login.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS users (
    id              bigserial PRIMARY KEY,
    tenant_id       bigint      NOT NULL REFERENCES tenants(id),
    email           text,
    display_name    text        NOT NULL DEFAULT '',
    status          text        NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'disabled', 'deleted')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);

-- Composite uniqueness so api_keys can FK on (tenant_id, id) — defeats
-- cross-tenant binding (codex synthesized plan §2.4.4).
CREATE UNIQUE INDEX uq_users_tenant_id_id ON users (tenant_id, id);

CREATE INDEX idx_users_tenant_status ON users (tenant_id, status)
    WHERE deleted_at IS NULL;

-- email uniqueness within tenant; case-insensitive; only when set + not deleted.
CREATE UNIQUE INDEX uq_users_tenant_email
    ON users (tenant_id, lower(email))
    WHERE email IS NOT NULL AND deleted_at IS NULL;

COMMENT ON TABLE users IS 'L0 minimum (2026-04-30 N+4a) end-user identity. No password column — HUAKAI authenticates via api_keys.';

-- ----------------------------------------------------------------------------
-- Table: api_keys
-- Inbound bearer-token storage. key_hash is bcrypt; key_prefix is the
-- first 16 chars of the plaintext for indexed lookup (no plaintext stored).
-- last_used_at is NOT updated in N+4a per CMB-7 (Auth is read-only); a
-- later slice may add an event-driven async writer.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS api_keys (
    id              bigserial PRIMARY KEY,
    tenant_id       bigint      NOT NULL REFERENCES tenants(id),
    user_id         bigint      NOT NULL,
    name            text        NOT NULL,
    key_hash        text        NOT NULL,
    key_prefix      text        NOT NULL,
    status          text        NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'disabled', 'revoked', 'expired')),
    expires_at      timestamptz,
    last_used_at    timestamptz,
    revoked_at      timestamptz,
    revoked_reason  text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz,
    -- Composite FK to users — same tenant_id; defends cross-tenant
    -- user_id misbinding (codex synthesized plan §2.4.4).
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)
);

-- Hot-path resolver lookup: prefix-only (resolver doesn't know tenant_id
-- at lookup time — that's what it's resolving). Codex N+4a P2 finding:
-- a (tenant_id, key_prefix) composite index can't service prefix-only
-- queries efficiently as the api_keys table grows.
CREATE INDEX idx_api_keys_prefix_active ON api_keys (key_prefix)
    WHERE deleted_at IS NULL AND status = 'active';

-- Operator/admin lookup: list keys for one tenant (kept for admin UI).
CREATE INDEX idx_api_keys_tenant_prefix ON api_keys (tenant_id, key_prefix)
    WHERE deleted_at IS NULL;

-- Operator queries: list all keys for a user.
CREATE INDEX idx_api_keys_user_status ON api_keys (tenant_id, user_id, status)
    WHERE deleted_at IS NULL;

-- Background expiry sweep (Phase E worker).
CREATE INDEX idx_api_keys_expires_at ON api_keys (expires_at)
    WHERE expires_at IS NOT NULL AND deleted_at IS NULL;

COMMENT ON TABLE api_keys IS 'L0 minimum (2026-04-30 N+4a) inbound bearer storage. key_hash is bcrypt; key_prefix indexed for tenant-scoped lookup. CMB-5: no plaintext bearer EVER persisted; CMB-7: last_used_at NOT updated synchronously in N+4.';
COMMENT ON COLUMN api_keys.key_prefix IS 'First 16 chars of plaintext bearer (incl. "hk_live_" or "hk_test_" namespace prefix). Indexed for hot-path; insufficient on its own to authenticate.';
COMMENT ON COLUMN api_keys.key_hash IS 'bcrypt hash of the full plaintext bearer (cost=10). NEVER append to logs; CMB-5.';

COMMIT;
