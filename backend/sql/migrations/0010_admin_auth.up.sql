-- Slice 2 (N+4b2): Admin auth surface.
-- Per docs/process/plans/2026-05-01-n4b-admin-keys.md §Scope A + D1/D5.
--
-- Purpose:
--   * admin_tokens: bcrypt-hashed operator credentials, separate from
--     api_keys. Mirrors api_keys shape so reviewers don't learn a new
--     pattern, but kept on its own table to prevent the hot inbound
--     resolver from accidentally treating an admin token as a customer
--     bearer (CMB-1).
--   * admin_audit_events: append-only audit row for every admin action
--     (issue / revoke / list / login). Shape aligns with existing
--     domain-scoped audit tables (oauth_refresh_audit_events,
--     rate_limit_audit_events) and the OpenAPI AuditEvent schema at
--     docs/openapi/openapi.yaml.
--
-- Migration is additive; no existing rows are touched.

BEGIN;

-- ----------------------------------------------------------------------------
-- Table: admin_tokens
-- One row per operator credential. Two roles at L0:
--   - platform_admin: can issue/revoke/list keys for ANY tenant
--     (scope_tenant_id IS NULL)
--   - tenant_operator: confined to its scope_tenant_id; cross-tenant
--     calls return 403
-- bootstrap=true marks the env-var-seeded first admin; cleanup recommended
-- after the operator issues a real admin token from it.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS admin_tokens (
    id              bigserial   PRIMARY KEY,
    name            text        NOT NULL,
    key_hash        text        NOT NULL,
    key_prefix      text        NOT NULL,             -- first 16 chars; mirrors api_keys
    role            text        NOT NULL DEFAULT 'tenant_operator'
                                CHECK (role IN ('platform_admin', 'tenant_operator')),
    scope_tenant_id bigint      REFERENCES tenants(id),
    bootstrap       boolean     NOT NULL DEFAULT false,
    status          text        NOT NULL DEFAULT 'active'
                                CHECK (status IN ('active', 'disabled', 'revoked')),
    expires_at      timestamptz,
    last_used_at    timestamptz,
    revoked_at      timestamptz,
    revoked_reason  text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz,
    CONSTRAINT scope_tenant_consistency
        CHECK ((role = 'platform_admin' AND scope_tenant_id IS NULL) OR
               (role = 'tenant_operator' AND scope_tenant_id IS NOT NULL))
);

CREATE INDEX idx_admin_tokens_prefix
    ON admin_tokens (key_prefix)
    WHERE deleted_at IS NULL AND status = 'active';

COMMENT ON TABLE admin_tokens IS
    'Slice 2 (N+4b2): operator credentials. Separate table from api_keys; CMB-1 keeps the inbound resolver away from this table. Bootstrap row is env-var seeded; rotate before public exposure.';
COMMENT ON COLUMN admin_tokens.key_hash IS 'bcrypt hash of the full plaintext bearer (cost=10). NEVER appended to logs; CMB-5.';
COMMENT ON COLUMN admin_tokens.key_prefix IS 'First 16 chars of plaintext bearer (e.g. "hk_admin_xxxxxxxx"). Indexed for hot-path; insufficient on its own to authenticate.';

-- ----------------------------------------------------------------------------
-- Table: admin_audit_events
-- Append-only. tenant_id is NULL when a platform_admin acts cross-tenant
-- (e.g. issuing a key for any tenant). actor_id is the admin_tokens.id
-- stringified so future cross-tenant joins stay flexible.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS admin_audit_events (
    id           bigserial   PRIMARY KEY,
    tenant_id    bigint      REFERENCES tenants(id),  -- NULL for cross-tenant platform actions
    actor_id     text        NOT NULL,
    actor_role   text        NOT NULL CHECK (actor_role IN ('platform_admin', 'tenant_operator')),
    -- Codex N+4b1 pass-3 P2-B: action and target_type are CHECK-bounded.
    -- Open-ended TEXT lets a typo silently bypass the rate-limit window
    -- (CountIssuanceInWindow keys on action='issue_api_key'). New actions
    -- must be added to BOTH the CHECK constraint AND any rate-limit
    -- queries that depend on the action enum.
    action       text        NOT NULL CHECK (action IN
                                ('issue_api_key', 'revoke_api_key', 'list_api_keys',
                                 'issue_admin_token', 'revoke_admin_token',
                                 'admin_login')),
    target_type  text        NOT NULL CHECK (target_type IN
                                ('api_key', 'admin_token', 'tenant', 'user')),
    target_id    bigint,
    request_id   text,                                -- chi middleware-set
    reason       text,
    payload      jsonb       NOT NULL DEFAULT '{}'::jsonb,  -- redacted: NEVER plaintext or hash
    occurred_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_admin_audit_events_tenant_time
    ON admin_audit_events (tenant_id, occurred_at DESC);

-- Used by the issuance rate-limit window (D4: 30 issues / hour / actor).
CREATE INDEX idx_admin_audit_events_actor_action_time
    ON admin_audit_events (actor_id, action, occurred_at DESC);

COMMENT ON TABLE admin_audit_events IS
    'Slice 2 (N+4b2): append-only admin audit. Aligns with OpenAPI AuditEvent schema. payload jsonb MUST NEVER contain plaintext bearer or key_hash (CMB-5).';

COMMIT;
