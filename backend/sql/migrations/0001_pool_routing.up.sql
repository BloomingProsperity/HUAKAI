-- HUAKAI Phase 2 Schema Lock: pool-routing
-- ============================================================================
-- Locks the schema surface required by docs/specs/pool-routing.md (F-POOL-001).
-- DR-008 §1: schema fragments may be locked only after spec is Released.
-- DR-001: every primary table carries non-null tenant_id.
-- DR-006: PostgreSQL with sqlc; no ORM; row-level locks via SELECT FOR UPDATE.
-- ============================================================================

-- ----------------------------------------------------------------------------
-- Table: tenants
-- ----------------------------------------------------------------------------
-- A tenant scope; MVP hard-codes a single 'default' tenant per DR-001.
-- All primary tables FK to tenants.id and carry a tenant_id column.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS tenants (
    id              bigserial PRIMARY KEY,
    name            text        NOT NULL,
    status          text        NOT NULL DEFAULT 'active',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);
CREATE UNIQUE INDEX uq_tenants_name ON tenants (name) WHERE deleted_at IS NULL;
COMMENT ON TABLE tenants IS 'Multi-tenant scope; MVP uses single default tenant per DR-001.';

-- ----------------------------------------------------------------------------
-- Table: providers
-- ----------------------------------------------------------------------------
-- Upstream LLM provider catalog (e.g. anthropic, openai, gemini, bedrock).
-- Tenant-scoped to allow per-tenant provider catalog customization.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS providers (
    id              bigserial PRIMARY KEY,
    tenant_id       bigint      NOT NULL REFERENCES tenants(id),
    code            text        NOT NULL,    -- e.g. 'anthropic', 'openai'
    display_name    text        NOT NULL,
    upstream_protocol  text     NOT NULL,    -- 'anthropic_messages', 'openai_chat', 'openai_responses', 'gemini', 'bedrock'
    enabled         boolean     NOT NULL DEFAULT true,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);
CREATE UNIQUE INDEX uq_providers_tenant_code ON providers (tenant_id, code) WHERE deleted_at IS NULL;
COMMENT ON TABLE providers IS 'Upstream LLM provider catalog. Code drives upstream-protocol adapter choice (F-PROTO-001).';

-- ----------------------------------------------------------------------------
-- Table: pool_groups
-- ----------------------------------------------------------------------------
-- Logical grouping of Provider Accounts presented as one capacity surface.
-- Routes select Pool Groups; Pool Groups contain Provider Accounts via channels.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS pool_groups (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    name                        text        NOT NULL,
    routing_policy_version      text        NOT NULL DEFAULT '1.0',
    -- F-POOL-001 §5 Phase B: Top-K policy
    top_k_default               integer     NOT NULL DEFAULT 1
                                    CHECK (top_k_default BETWEEN 1 AND 10),
    -- F-POOL-001 §10 Q4: capability default
    capability_default          text        NOT NULL DEFAULT 'exact_capability_only'
                                    CHECK (capability_default IN ('exact_capability_only', 'safe_equivalent_allowed')),
    -- F-POOL-001 §10 Q1: forced-route visibility
    allow_tenant_operator_force boolean     NOT NULL DEFAULT false,
    -- F-POOL-001 §10: last-healthy exemption
    allow_last_resort           boolean     NOT NULL DEFAULT false,
    -- Wait budgets
    sticky_wait_max_waiting     integer     NOT NULL DEFAULT 2,   -- shorter
    fallback_wait_max_waiting   integer     NOT NULL DEFAULT 8,   -- longer
    sticky_wait_timeout_ms      integer     NOT NULL DEFAULT 5000,
    fallback_wait_timeout_ms    integer     NOT NULL DEFAULT 30000,
    -- Forced-route rate limit (SaaS edition)
    forced_route_rate_limit_per_hour integer NOT NULL DEFAULT 5,
    enabled                     boolean     NOT NULL DEFAULT true,
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    deleted_at                  timestamptz
);
CREATE UNIQUE INDEX uq_pool_groups_tenant_name ON pool_groups (tenant_id, name) WHERE deleted_at IS NULL;
COMMENT ON TABLE pool_groups IS 'F-POOL-001 §1: logical capacity grouping; routing policy + Q1..Q4 owner decisions live here.';

-- ----------------------------------------------------------------------------
-- Table: channels
-- ----------------------------------------------------------------------------
-- Per-pool routing/eligibility filter. Channel limits which Provider Accounts
-- a request can reach within a Pool Group.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS channels (
    id              bigserial PRIMARY KEY,
    tenant_id       bigint      NOT NULL REFERENCES tenants(id),
    pool_group_id   bigint      NOT NULL REFERENCES pool_groups(id),
    name            text        NOT NULL,
    -- Failover status code list (HUAKAI configurable per Pool/Channel)
    failover_status_codes integer[] NOT NULL DEFAULT ARRAY[401, 403, 429, 529]::integer[],
    enabled         boolean     NOT NULL DEFAULT true,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);
CREATE UNIQUE INDEX uq_channels_tenant_pool_name ON channels (tenant_id, pool_group_id, name) WHERE deleted_at IS NULL;
COMMENT ON TABLE channels IS 'F-POOL-001: Channel filter inside Pool Group. Failover status codes configurable per Channel (HUAKAI improvement over Sub2API hardcoded list).';

-- ----------------------------------------------------------------------------
-- Table: provider_accounts
-- ----------------------------------------------------------------------------
-- Upstream credential + capacity unit. The unit selected by F-POOL-001.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS provider_accounts (
    id                      bigserial PRIMARY KEY,
    tenant_id               bigint      NOT NULL REFERENCES tenants(id),
    provider_id             bigint      NOT NULL REFERENCES providers(id),
    channel_id              bigint      NOT NULL REFERENCES channels(id),
    name                    text        NOT NULL,
    -- Account type
    account_type            text        NOT NULL CHECK (account_type IN ('oauth', 'api_key', 'service_account', 'upstream_static')),
    -- Lifecycle (F-POOL-001 §Phase A gate 2)
    enabled                 boolean     NOT NULL DEFAULT true,
    expires_at              timestamptz,
    -- Health state (F-POOL-001 §Phase A gate 7)
    health_state            text        NOT NULL DEFAULT 'operational'
                                CHECK (health_state IN ('operational', 'degraded', 'failed', 'cooling_down', 'error')),
    health_state_until      timestamptz,    -- when current state auto-clears
    -- Credential state (F-POOL-001 §Phase A gate 6)
    credential_state        text        NOT NULL DEFAULT 'valid'
                                CHECK (credential_state IN ('valid', 'refreshing', 'refreshing_with_grace', 'refresh_failed', 'revoked')),
    credentials             jsonb       NOT NULL DEFAULT '{}'::jsonb,    -- redacted at rest per DR; never logged
    -- Concurrency (F-POOL-001 §6.13 atomic admission)
    cap_concurrency         integer     NOT NULL DEFAULT 4 CHECK (cap_concurrency >= 1),
    in_flight_count         integer     NOT NULL DEFAULT 0 CHECK (in_flight_count >= 0),
    cap_queue_sticky        integer     NOT NULL DEFAULT 2,
    cap_queue_fallback      integer     NOT NULL DEFAULT 8,
    queue_depth             integer     NOT NULL DEFAULT 0 CHECK (queue_depth >= 0),
    -- Selection ordering (F-POOL-001 §5.2 Layer 2)
    priority                integer     NOT NULL DEFAULT 100,
    last_dispatch_at        timestamptz,
    -- Capability flags (F-POOL-001 §Phase A gate 5)
    model_allow_list        text[]      NOT NULL DEFAULT ARRAY[]::text[],
    capability_flags        text[]      NOT NULL DEFAULT ARRAY[]::text[],   -- 'tool_use', 'vision', 'reasoning_high', etc.
    -- Quota (Sub2API S3 atomic status flip; HUAKAI O8 cross-threshold)
    cap_quota_total         numeric(20,8),
    quota_used_total        numeric(20,8) NOT NULL DEFAULT 0,
    cap_quota_daily         numeric(20,8),
    quota_used_daily        numeric(20,8) NOT NULL DEFAULT 0,
    quota_window_daily_start timestamptz,
    cap_quota_weekly        numeric(20,8),
    quota_used_weekly       numeric(20,8) NOT NULL DEFAULT 0,
    quota_window_weekly_start timestamptz,
    quota_status            text        NOT NULL DEFAULT 'active'
                                CHECK (quota_status IN ('active', 'exhausted', 'paused')),
    -- Audit
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    deleted_at              timestamptz,
    created_by_actor        text,    -- operator id; NULL for system
    last_modified_by_actor  text
);
CREATE UNIQUE INDEX uq_provider_accounts_tenant_name ON provider_accounts (tenant_id, name) WHERE deleted_at IS NULL;
CREATE INDEX idx_provider_accounts_pool_dispatch
    ON provider_accounts (tenant_id, channel_id, enabled, health_state, last_dispatch_at NULLS FIRST)
    WHERE deleted_at IS NULL;
CREATE INDEX idx_provider_accounts_health_until
    ON provider_accounts (health_state, health_state_until)
    WHERE health_state_until IS NOT NULL;
COMMENT ON TABLE provider_accounts IS 'F-POOL-001: upstream credential + capacity unit. Selected by 5-layer algorithm. Credentials JSONB redacted at rest.';
COMMENT ON COLUMN provider_accounts.in_flight_count IS 'Atomic counter for slot acquisition; row-locked via SELECT FOR UPDATE during F-POOL-001 §Phase C.';

-- ----------------------------------------------------------------------------
-- Table: pool_slot_acquisitions
-- ----------------------------------------------------------------------------
-- Per-acquisition record for slot release idempotency (F-POOL-001 invariant I2).
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS pool_slot_acquisitions (
    id                      bigserial PRIMARY KEY,
    tenant_id               bigint      NOT NULL REFERENCES tenants(id),
    provider_account_id     bigint      NOT NULL REFERENCES provider_accounts(id),
    acquisition_token       uuid        NOT NULL,
    -- Coupling to Quota+Billing claim row (Pattern B writeback)
    claim_id                bigint,    -- FK to billing_ledger_claims (locked once F-BILL-001 Released)
    attempt_seq             integer     NOT NULL DEFAULT 1,
    -- Lease for long streams (F-POOL-001 §5.1 lease + heartbeat)
    heartbeat_at            timestamptz,
    lease_expires_at        timestamptz NOT NULL,
    -- Status
    status                  text        NOT NULL DEFAULT 'acquired'
                                CHECK (status IN ('acquired', 'released_success', 'released_failure', 'orphan_swept')),
    released_at             timestamptz,
    release_reason          text,
    -- Audit
    created_at              timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_slot_acq_token ON pool_slot_acquisitions (acquisition_token);
CREATE INDEX idx_slot_acq_account_status ON pool_slot_acquisitions (provider_account_id, status);
CREATE INDEX idx_slot_acq_orphan_sweep
    ON pool_slot_acquisitions (status, lease_expires_at)
    WHERE status = 'acquired';
COMMENT ON TABLE pool_slot_acquisitions IS 'F-POOL-001 I2: idempotent slot release via UUID token. Orphan sweep finds status=acquired AND lease_expires_at < now().';

-- ----------------------------------------------------------------------------
-- Table: sticky_bindings
-- ----------------------------------------------------------------------------
-- Sticky session affinity binding (F-POOL-001 Layer 1.5 / 1.5b).
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS sticky_bindings (
    id                  bigserial PRIMARY KEY,
    tenant_id           bigint      NOT NULL REFERENCES tenants(id),
    session_hash        text        NOT NULL,    -- derived per F-POOL-001 §Phase A
    model               text        NOT NULL,    -- bound per (session, model) pair
    provider_account_id bigint      NOT NULL REFERENCES provider_accounts(id),
    expires_at          timestamptz NOT NULL,
    refreshed_at        timestamptz NOT NULL DEFAULT now(),
    created_at          timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_sticky_tenant_session_model ON sticky_bindings (tenant_id, session_hash, model);
CREATE INDEX idx_sticky_expires_at ON sticky_bindings (expires_at);
COMMENT ON TABLE sticky_bindings IS 'F-POOL-001 Layer 1.5/1.5b: sticky session affinity. session_hash derived from cache_control / metadata.user_id / SessionContext per Phase A.';

-- ----------------------------------------------------------------------------
-- Table: routes
-- ----------------------------------------------------------------------------
-- User Group → Pool Group routing rule.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS routes (
    id                      bigserial PRIMARY KEY,
    tenant_id               bigint      NOT NULL REFERENCES tenants(id),
    name                    text        NOT NULL,
    -- Match conditions (User Group, model pattern)
    user_group_match        text        NOT NULL,    -- e.g. 'default', 'premium'
    model_pattern_match     text        NOT NULL,    -- e.g. 'claude-*', '*'
    -- Selection target
    pool_group_id           bigint      NOT NULL REFERENCES pool_groups(id),
    -- Per-Route policy (F-POOL-001 §10 Q2 — sticky budget at Route by default)
    sticky_wait_max_override    integer,    -- NULL = inherit from pool_group
    fallback_wait_max_override  integer,
    -- Capability policy override (Q4)
    capability_policy_override  text CHECK (capability_policy_override IS NULL OR capability_policy_override IN ('exact_capability_only', 'safe_equivalent_allowed')),
    -- Top-K policy override
    top_k_override          integer CHECK (top_k_override IS NULL OR top_k_override BETWEEN 1 AND 10),
    -- Optional score weights (default 0; HUAKAI improvement)
    weight_priority             integer NOT NULL DEFAULT 1,
    weight_load_rate            integer NOT NULL DEFAULT 0,
    weight_last_used            integer NOT NULL DEFAULT 0,
    weight_recent_error_rate    integer NOT NULL DEFAULT 0,
    weight_recent_latency       integer NOT NULL DEFAULT 0,
    weight_quota_headroom       integer NOT NULL DEFAULT 0,
    weight_fairness_debt        integer NOT NULL DEFAULT 0,
    weight_snapshot_freshness   integer NOT NULL DEFAULT 0,
    -- Priority ordering (lower = match first)
    match_priority          integer     NOT NULL DEFAULT 100,
    enabled                 boolean     NOT NULL DEFAULT true,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    deleted_at              timestamptz
);
CREATE UNIQUE INDEX uq_routes_tenant_name ON routes (tenant_id, name) WHERE deleted_at IS NULL;
CREATE INDEX idx_routes_match_order ON routes (tenant_id, match_priority, enabled) WHERE deleted_at IS NULL;
COMMENT ON TABLE routes IS 'F-POOL-001 §5.2: routing rule. Per-Route override of Pool defaults per Q2. Optional score weights default 0 (compatibility mode).';

-- ----------------------------------------------------------------------------
-- Table: model_routing_overrides
-- ----------------------------------------------------------------------------
-- Per-model Account list (F-POOL-001 Layer 1: Model Routing config).
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS model_routing_overrides (
    id                  bigserial PRIMARY KEY,
    tenant_id           bigint      NOT NULL REFERENCES tenants(id),
    pool_group_id       bigint      NOT NULL REFERENCES pool_groups(id),
    model               text        NOT NULL,
    -- Ordered list of provider_account ids that may serve this model
    provider_account_ids bigint[]   NOT NULL,
    enabled             boolean     NOT NULL DEFAULT true,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    deleted_at          timestamptz
);
CREATE UNIQUE INDEX uq_model_routing_pool_model ON model_routing_overrides (pool_group_id, model) WHERE deleted_at IS NULL;
COMMENT ON TABLE model_routing_overrides IS 'F-POOL-001 Layer 1: Model Routing config. Empty array = no override; selection skips Layer 1 and goes to Layer 1.5b.';

-- ----------------------------------------------------------------------------
-- Table: pool_routing_audit_events
-- ----------------------------------------------------------------------------
-- Audit trail for forced-route overrides + other security-grade events.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS pool_routing_audit_events (
    id                  bigserial PRIMARY KEY,
    tenant_id           bigint      NOT NULL REFERENCES tenants(id),
    event_type          text        NOT NULL CHECK (event_type IN
                            ('forced_route_invoked', 'forced_route_authorization_failed',
                             'allow_last_resort_used', 'pool_exhausted',
                             'capability_safe_equivalent_used', 'sticky_binding_broken',
                             'orphan_sweep_recovery')),
    pool_group_id       bigint      REFERENCES pool_groups(id),
    provider_account_id bigint      REFERENCES provider_accounts(id),
    request_id          text,
    actor_id            text,        -- operator who invoked (forced_route only)
    actor_role          text,        -- 'platform_admin' | 'tenant_operator'
    reason              text,
    payload             jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_pool_audit_tenant_type_time
    ON pool_routing_audit_events (tenant_id, event_type, created_at DESC);
CREATE INDEX idx_pool_audit_actor_time
    ON pool_routing_audit_events (actor_id, created_at DESC) WHERE actor_id IS NOT NULL;
COMMENT ON TABLE pool_routing_audit_events IS 'F-POOL-001 §Audit: structured audit trail. Forced-route + last-resort + safe-equivalent uses captured here.';

-- ----------------------------------------------------------------------------
-- Outbox: scheduler invalidation
-- ----------------------------------------------------------------------------
-- Cross-threshold transactional outbox (F-OBS-001 synthesis O5).
-- Lives here because pool selection consumes scheduler outbox messages.
-- F-OBS-001 schema fragment will reference the same table.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS scheduler_outbox (
    id                  bigserial PRIMARY KEY,
    tenant_id           bigint      NOT NULL REFERENCES tenants(id),
    event_type          text        NOT NULL CHECK (event_type IN
                            ('account_quota_changed', 'account_health_changed',
                             'pool_routing_config_changed', 'sticky_binding_invalidated',
                             'forced_route_authorization_changed')),
    pool_group_id       bigint,
    provider_account_id bigint,
    payload             jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at          timestamptz NOT NULL DEFAULT now(),
    -- Consumer state
    consumed_at         timestamptz,
    consumer_id         text,
    -- Lag observability
    lag_alert_threshold_seconds integer NOT NULL DEFAULT 60
);
CREATE INDEX idx_scheduler_outbox_unconsumed
    ON scheduler_outbox (tenant_id, created_at)
    WHERE consumed_at IS NULL;
CREATE INDEX idx_scheduler_outbox_lag_alert
    ON scheduler_outbox (created_at)
    WHERE consumed_at IS NULL;
COMMENT ON TABLE scheduler_outbox IS 'Transactional outbox for cache invalidation. Consumer reads ORDER BY created_at; idempotent invalidation. Lag > threshold triggers alert (F-OBS-001 O5).';

-- ----------------------------------------------------------------------------
-- Indexes summary
-- ----------------------------------------------------------------------------
-- Selection hot path:
--   idx_provider_accounts_pool_dispatch  - covers Layer 1 / 2 candidate scan
--   idx_sticky_tenant_session_model      - covers Layer 1.5 / 1.5b lookup
--   idx_routes_match_order               - covers Phase A route matching
-- Recovery path:
--   idx_slot_acq_orphan_sweep            - covers orphan sweep query
--   idx_pool_audit_tenant_type_time      - covers operator dashboards
--   idx_scheduler_outbox_unconsumed      - covers consumer pull
-- ----------------------------------------------------------------------------

-- ----------------------------------------------------------------------------
-- Schema lock metadata
-- ----------------------------------------------------------------------------
-- Locked: 2026-04-28
-- Spec source: docs/specs/pool-routing.md @ Status=Released
-- Migration order: 0001 (initial). Future migrations forward-only.
-- Constraint: any field change requires new DR + new spec revision.
-- ----------------------------------------------------------------------------
