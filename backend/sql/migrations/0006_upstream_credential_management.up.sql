-- HUAKAI Phase 2 Schema Lock: upstream-credential-management
-- ============================================================================
-- Locks the schema surface required by docs/specs/upstream-credential-management.md
-- (F-AUTH-005).
-- DR-008 §1: schema fragments locked only after spec is Released.
--
-- Most F-AUTH-005 state lives on provider_accounts.credentials (jsonb) which
-- already exists in pool-routing.sql. This fragment adds CAS columns + audit
-- trail + mimicry policy + storm budget tracking.
-- ============================================================================

-- ----------------------------------------------------------------------------
-- ALTER TABLE: provider_accounts — F-AUTH-005 credential CAS + fingerprint
-- ----------------------------------------------------------------------------
-- HUAKAI invariant A8: CAS on _token_version for credential persistence.
-- Codex C5: token_version was a cache marker in Sub2API; HUAKAI promotes to
-- persistence precondition.
-- ----------------------------------------------------------------------------
ALTER TABLE provider_accounts
    ADD COLUMN IF NOT EXISTS token_version           integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS refresh_token_fingerprint text,    -- SHA(refresh_token + tenant_id); NEVER plaintext
    ADD COLUMN IF NOT EXISTS last_refresh_at         timestamptz,
    ADD COLUMN IF NOT EXISTS last_refresh_outcome    text
                                CHECK (last_refresh_outcome IS NULL OR last_refresh_outcome IN
                                    ('cache_hit', 'refresh_lock_held', 'refresh_succeeded',
                                     'refresh_token_rotated', 'db_version_conflict',
                                     'invalid_grant_race_recovered', 'storm_budget_exhausted',
                                     'cas_lost', 'token_malformed', 'oauth_401_force_refresh',
                                     'permanent_disable')),
    ADD COLUMN IF NOT EXISTS oauth_endpoint_health   text     NOT NULL DEFAULT 'operational'
                                CHECK (oauth_endpoint_health IN
                                    ('operational', 'degraded', 'circuit_open'));

CREATE INDEX IF NOT EXISTS idx_provider_accounts_refresh_at
    ON provider_accounts (last_refresh_at)
    WHERE last_refresh_at IS NOT NULL;

COMMENT ON COLUMN provider_accounts.token_version IS 'F-AUTH-005 invariant A8: CAS guard for credential persistence. Increments on every credential write.';
COMMENT ON COLUMN provider_accounts.refresh_token_fingerprint IS 'F-AUTH-005 H3: SHA(refresh_token + tenant_id). Used for diagnosing refresh races without leaking tokens. Never plaintext.';
COMMENT ON COLUMN provider_accounts.oauth_endpoint_health IS 'F-AUTH-005 H4 provider-endpoint-scope: circuit-breaker state for upstream OAuth endpoint.';

-- ----------------------------------------------------------------------------
-- Table: oauth_refresh_audit_events
-- ----------------------------------------------------------------------------
-- Audit trail for token refresh outcomes. Append-only. Token-leakage-safe
-- (no credential bytes ever in any column).
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS oauth_refresh_audit_events (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    provider_account_id         bigint      NOT NULL REFERENCES provider_accounts(id),
    -- Outcome (matches last_refresh_outcome enum + finer detail)
    outcome                     text        NOT NULL CHECK (outcome IN
                                    ('cache_hit', 'refresh_lock_held', 'refresh_succeeded',
                                     'refresh_token_rotated', 'db_version_conflict',
                                     'invalid_grant_race_recovered', 'storm_budget_exhausted',
                                     'cas_lost', 'token_malformed', 'oauth_401_force_refresh',
                                     'permanent_disable', 'mimicry_applied')),
    -- Storm budget scope (when outcome='storm_budget_exhausted')
    storm_scope                 text CHECK (storm_scope IS NULL OR storm_scope IN
                                    ('account', 'provider_endpoint', 'global')),
    -- Token rotation tracking (when outcome='refresh_token_rotated')
    old_token_fingerprint       text,
    new_token_fingerprint       text,
    -- Mimicry-specific (when outcome='mimicry_applied')
    mimicry_components_applied  text[],    -- e.g. {'system_rewrite', 'cache_strip', 'tool_obfuscation'}
    mimicry_policy_version      text,
    request_id                  text,
    client_protocol             text,
    model                       text,
    -- Sanitized error (no token bytes)
    error_class                 text,
    error_message_redacted      text,    -- run through OAuth error sanitizer per H5
    -- Audit
    occurred_at                 timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_oauth_audit_account_time
    ON oauth_refresh_audit_events (provider_account_id, occurred_at DESC);
CREATE INDEX idx_oauth_audit_outcome_time
    ON oauth_refresh_audit_events (outcome, occurred_at DESC);
CREATE INDEX idx_oauth_audit_tenant_outcome_time
    ON oauth_refresh_audit_events (tenant_id, outcome, occurred_at DESC);
COMMENT ON TABLE oauth_refresh_audit_events IS 'F-AUTH-005: append-only audit trail. Token-leakage-safe (sanitized errors only). Mimicry events recorded with components + policy version.';

-- ----------------------------------------------------------------------------
-- Table: oauth_storm_budget
-- ----------------------------------------------------------------------------
-- Three-scope storm controller state per F-AUTH-005 H4 / Codex C7.
-- Updated atomically during refresh; readers see latest budget.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS oauth_storm_budget (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    -- Scope identity
    scope_type                  text        NOT NULL CHECK (scope_type IN
                                    ('account', 'provider_endpoint', 'global')),
    -- For 'account' scope: provider_account_id
    -- For 'provider_endpoint' scope: provider + oauth_endpoint url-fingerprint
    -- For 'global' scope: NULL identifiers
    provider_account_id         bigint      REFERENCES provider_accounts(id),
    provider_code               text,        -- when scope_type='provider_endpoint'
    oauth_endpoint_fingerprint  text,        -- SHA of endpoint URL
    -- Budget configuration
    cap_concurrent_refreshes    integer     NOT NULL DEFAULT 1,
    cap_refreshes_per_minute    integer     NOT NULL DEFAULT 60,
    -- Current state
    current_in_flight           integer     NOT NULL DEFAULT 0,
    refreshes_in_window         integer     NOT NULL DEFAULT 0,
    window_start                timestamptz NOT NULL DEFAULT now(),
    -- Circuit breaker
    circuit_open_until          timestamptz,
    -- Audit
    last_updated_at             timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_oauth_storm_account
    ON oauth_storm_budget (tenant_id, provider_account_id)
    WHERE scope_type = 'account';
CREATE UNIQUE INDEX uq_oauth_storm_endpoint
    ON oauth_storm_budget (tenant_id, provider_code, oauth_endpoint_fingerprint)
    WHERE scope_type = 'provider_endpoint';
CREATE UNIQUE INDEX uq_oauth_storm_global
    ON oauth_storm_budget (tenant_id, scope_type)
    WHERE scope_type = 'global';
COMMENT ON TABLE oauth_storm_budget IS 'F-AUTH-005 H4: three-scope storm controller state. Per-account, per-(provider, endpoint), and per-tenant global budgets.';

-- ----------------------------------------------------------------------------
-- Table: mimicry_policy
-- ----------------------------------------------------------------------------
-- Per-Pool Claude Code mimicry configuration. Operator-confirmed opt-in.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS mimicry_policy (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    pool_group_id               bigint      NOT NULL REFERENCES pool_groups(id),
    enabled                     boolean     NOT NULL DEFAULT false,
    -- Operator must paste legal review document ID before enabling (F-AUTH-005 H6)
    legal_review_id             text,
    legal_review_received_at    timestamptz,
    -- Policy version (for audit attribution)
    policy_version              text        NOT NULL DEFAULT '1.0',
    -- Per-mimicry-component toggles (granular control)
    apply_system_rewrite        boolean     NOT NULL DEFAULT true,
    apply_cache_control_strip   boolean     NOT NULL DEFAULT true,
    apply_breakpoints           boolean     NOT NULL DEFAULT true,
    apply_tool_obfuscation      boolean     NOT NULL DEFAULT true,
    apply_metadata_user_id      boolean     NOT NULL DEFAULT true,
    -- Operator audit
    enabled_by_actor            text,
    enabled_at                  timestamptz,
    -- Constraint: enabled requires legal_review_id
    CONSTRAINT ck_mimicry_legal_review
        CHECK (NOT enabled OR legal_review_id IS NOT NULL)
);
CREATE UNIQUE INDEX uq_mimicry_pool ON mimicry_policy (pool_group_id);
COMMENT ON TABLE mimicry_policy IS 'F-AUTH-005 H6: per-Pool Claude Code mimicry. Constraint: enabled requires legal_review_id (no enabling without legal review document attached).';

-- ----------------------------------------------------------------------------
-- Indexes summary
-- ----------------------------------------------------------------------------
-- Hot path:
--   provider_accounts.token_version            - CAS guard during refresh
--   uq_oauth_storm_*                           - storm budget lookup per scope
--   idx_oauth_audit_account_time               - per-account refresh history
--   uq_mimicry_pool                            - mimicry config lookup
-- ----------------------------------------------------------------------------

-- ----------------------------------------------------------------------------
-- Schema lock metadata
-- ----------------------------------------------------------------------------
-- Locked: 2026-04-28
-- Spec source: docs/specs/upstream-credential-management.md @ Status=Released
-- Migration order: 0006 (after protocol-translation). Forward-only.
-- ----------------------------------------------------------------------------
