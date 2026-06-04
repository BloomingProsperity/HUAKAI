-- Model Registry.
-- NO credential field anywhere here.
-- Registry layer is read-only at request time. Admin writers
-- MUST UPDATE model_registry_snapshots.version IN THE SAME
-- TRANSACTION as any change to models/aliases/bindings/capabilities.

BEGIN;

-- ----------------------------------------------------------------------------
-- One-time additive index needed for the composite FK
-- (tenant_id, pool_group_id) on model_pool_bindings.
-- ----------------------------------------------------------------------------
CREATE UNIQUE INDEX IF NOT EXISTS uq_pool_groups_tenant_id_id
    ON pool_groups (tenant_id, id);

-- ----------------------------------------------------------------------------
-- Table: model_registry_snapshots
-- Per-tenant monotonic version counter. Admin writers increment in
-- the same TX as any registry change so RoutePlan.SnapshotVersion can
-- deterministically replay registry state at audit time.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS model_registry_snapshots (
    tenant_id        bigint      PRIMARY KEY REFERENCES tenants(id),
    version          bigint      NOT NULL DEFAULT 1 CHECK (version >= 1),
    reason           text,
    updated_by_actor text,
    updated_at       timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE model_registry_snapshots IS
    'Slice 2: per-tenant monotonic registry version. Admin writers MUST UPDATE version+1 in the same TX as any model/alias/binding/capability change. ResolveModel reads version into RoutePlan.SnapshotVersion (registry:<tid>:<v>) for audit replay.';

-- ----------------------------------------------------------------------------
-- Table: model_registry_tenant_policies
-- Explicit global-catalog inheritance switch. Replaces the
-- "tenant_id=0 sentinel" anti-pattern. Default is opt-in: false.
-- Tenant-scoped DISABLED rows ALWAYS block global fallback (explicit deny;
-- enforced in ResolveModelByAlias query).
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS model_registry_tenant_policies (
    tenant_id              bigint      PRIMARY KEY REFERENCES tenants(id),
    inherit_global_catalog boolean     NOT NULL DEFAULT false,
    updated_at             timestamptz NOT NULL DEFAULT now(),
    updated_by_actor       text
);
COMMENT ON TABLE model_registry_tenant_policies IS
    'Slice 2: opt-in global-catalog inheritance. inherit_global_catalog=true allows ResolveModel to fall through to scope=global rows on tenant miss. Tenant-scoped DISABLED rows always block (explicit deny per integration test #5).';

-- ----------------------------------------------------------------------------
-- Table: models
-- Canonical model identities. tenant_id NULL iff scope='global' (D20).
-- Pricing math is NOT here: pricing_class is a string
-- tag; decimal pricing lives in billing tables).
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS models (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NULL REFERENCES tenants(id),
    scope                       text        NOT NULL DEFAULT 'tenant'
                                    CHECK (scope IN ('tenant', 'global')),
    canonical_id                text        NOT NULL,
    protocol_family             text        NOT NULL CHECK (protocol_family IN
                                    ('anthropic_messages', 'openai_chat',
                                     'openai_responses', 'gemini')),
    default_provider_model_id   text        NOT NULL,
    default_context_window      integer     NOT NULL DEFAULT 0
                                    CHECK (default_context_window >= 0),
    default_request_timeout_ms  integer     NOT NULL DEFAULT 60000
                                    CHECK (default_request_timeout_ms > 0),
    pricing_class               text        NOT NULL DEFAULT 'standard',
    model_owner                 text        NOT NULL DEFAULT 'HUAKAI',
    model_created_at            timestamptz,
    status                      text        NOT NULL DEFAULT 'active'
                                    CHECK (status IN ('active', 'disabled', 'deleted')),
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    deleted_at                  timestamptz,
    CONSTRAINT models_scope_tenant_consistency
        CHECK ((scope = 'tenant' AND tenant_id IS NOT NULL)
            OR (scope = 'global' AND tenant_id IS NULL))
);
CREATE UNIQUE INDEX uq_models_tenant_canonical
    ON models (tenant_id, canonical_id)
    WHERE deleted_at IS NULL AND scope = 'tenant';
CREATE UNIQUE INDEX uq_models_global_canonical
    ON models (canonical_id)
    WHERE deleted_at IS NULL AND scope = 'global';
-- Composite FK target.
CREATE UNIQUE INDEX uq_models_tenant_id_id
    ON models (tenant_id, id) WHERE tenant_id IS NOT NULL;
COMMENT ON TABLE models IS
    'Slice 2: canonical model identities. provider-agnostic. tenant_id NULL iff scope=global (D20). pricing_class is a free-form tag; decimal pricing lives in billing (CMB-2 carve-out).';

-- ----------------------------------------------------------------------------
-- Table: model_aliases
-- Public alias -> model. Lookup uses public_alias_normalized (lower); the
-- as-seeded display string is preserved for audit.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS model_aliases (
    id                       bigserial PRIMARY KEY,
    tenant_id                bigint      NULL REFERENCES tenants(id),
    scope                    text        NOT NULL DEFAULT 'tenant'
                                CHECK (scope IN ('tenant', 'global')),
    model_id                 bigint      NOT NULL REFERENCES models(id),
    public_alias_normalized  text        NOT NULL,
    public_alias_display     text        NOT NULL,
    status                   text        NOT NULL DEFAULT 'active'
                                CHECK (status IN ('active', 'disabled', 'deleted')),
    disabled_reason          text,
    source                   text        NOT NULL DEFAULT 'operator',
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),
    deleted_at               timestamptz,
    CONSTRAINT aliases_scope_tenant_consistency
        CHECK ((scope = 'tenant' AND tenant_id IS NOT NULL)
            OR (scope = 'global' AND tenant_id IS NULL))
);
CREATE UNIQUE INDEX uq_aliases_tenant_alias
    ON model_aliases (tenant_id, public_alias_normalized)
    WHERE deleted_at IS NULL AND scope = 'tenant';
CREATE UNIQUE INDEX uq_aliases_global_alias
    ON model_aliases (public_alias_normalized)
    WHERE deleted_at IS NULL AND scope = 'global';
COMMENT ON TABLE model_aliases IS
    'Slice 2: (tenant, public_alias) -> model. Lookup is on normalized lower-case alias; display alias preserves operator-set casing for audit. Tenant-disabled rows always block global fallback (explicit deny).';

-- ----------------------------------------------------------------------------
-- Table: model_pool_bindings
-- Ordered model -> pool_group binding. ALWAYS tenant-scoped: pool_groups
-- are tenant-owned, so a "global binding" cannot exist without leaking one
-- tenant's pool_group_id to another tenant's resolver. Bindings are
-- tenant-local even for global models.
--
-- Inheritance flow for a global model: tenant T sets up its OWN
-- model_pool_bindings row pointing at T's pool_group + the shared global
-- model_id. The global alias resolves through inherit_global_catalog;
-- routing is always per-tenant.
--
-- rpm_limit/tpm_limit/max_parallel_requests are store-only here; request-time
-- rate gates enforce them. selection_mode is honored as strict_priority until
-- weighted execution is enabled.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS model_pool_bindings (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    -- FK to models(id) prevents orphaned bindings: a binding with a typo'd
    -- model_id cannot write successfully and then fail resolution.
    -- Tenant cross-check (binding.tenant must match model.tenant for
    -- scope='tenant' models) is enforced at admin-write time / resolver
    -- query; the simple FK here covers the orphan case.
    model_id                    bigint      NOT NULL REFERENCES models(id),
    pool_group_id               bigint      NOT NULL,
    priority                    integer     NOT NULL DEFAULT 100
                                    CHECK (priority >= 0),
    weight                      integer     NOT NULL DEFAULT 1
                                    CHECK (weight > 0),
    selection_mode              text        NOT NULL DEFAULT 'strict_priority'
                                    CHECK (selection_mode IN
                                        ('strict_priority', 'priority_weighted')),
    provider_model_id_override  text,
    rpm_limit                   integer
                                    CHECK (rpm_limit IS NULL OR rpm_limit >= 0),
    tpm_limit                   integer
                                    CHECK (tpm_limit IS NULL OR tpm_limit >= 0),
    max_parallel_requests       integer
                                    CHECK (max_parallel_requests IS NULL
                                        OR max_parallel_requests >= 0),
    fallback_class              text        NOT NULL DEFAULT 'normal'
                                    CHECK (fallback_class IN
                                        ('normal', 'context_window', 'safety',
                                         'quota', 'manual')),
    enabled                     boolean     NOT NULL DEFAULT true,
    disabled_reason             text,
    effective_from              timestamptz,
    effective_until             timestamptz,
    reason                      text        NOT NULL DEFAULT 'primary',
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    deleted_at                  timestamptz,
    -- Composite FK defends cross-tenant pool_group misbinding. Tenant_id is
    -- NOT NULL so this FK fires for every row.
    FOREIGN KEY (tenant_id, pool_group_id) REFERENCES pool_groups(tenant_id, id)
);
CREATE UNIQUE INDEX uq_bindings_tenant_model_pool
    ON model_pool_bindings (tenant_id, model_id, pool_group_id)
    WHERE deleted_at IS NULL;
CREATE INDEX idx_bindings_resolve
    ON model_pool_bindings (tenant_id, model_id, priority, weight, enabled)
    WHERE deleted_at IS NULL;
COMMENT ON TABLE model_pool_bindings IS
    'Slice 2: ordered tenant-scoped model -> pool_group binding. Reference citations: LiteLLM proxy types (rpm/tpm/max_parallel) + Portkey strategy.mode (selection_mode) + one-api ModelMapping (override) + envoy AIGatewayRouteRuleBackendRef (weight/priority) + LiteLLM typed-fallback (fallback_class). All verified via WebFetch 2026-04-30. Codex N+5a P1: scope column removed — global bindings would cross-tenant-leak pool ids.';

-- ----------------------------------------------------------------------------
-- Table: model_registry_capabilities
-- Per-model capability rows. capability_params jsonb supports
-- parameterized capabilities like reasoning_effort levels.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS model_registry_capabilities (
    id                bigserial PRIMARY KEY,
    tenant_id         bigint      NULL REFERENCES tenants(id),
    scope             text        NOT NULL DEFAULT 'tenant'
                            CHECK (scope IN ('tenant', 'global')),
    model_id          bigint      NOT NULL REFERENCES models(id),
    capability        text        NOT NULL,
    capability_value  text,
    capability_params jsonb       NOT NULL DEFAULT '{}'::jsonb,
    enabled           boolean     NOT NULL DEFAULT true,
    source            text        NOT NULL DEFAULT 'operator',
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    deleted_at        timestamptz,
    CONSTRAINT capabilities_scope_tenant_consistency
        CHECK ((scope = 'tenant' AND tenant_id IS NOT NULL)
            OR (scope = 'global' AND tenant_id IS NULL))
);
CREATE UNIQUE INDEX uq_caps_tenant_model_cap
    ON model_registry_capabilities (tenant_id, model_id, capability)
    WHERE deleted_at IS NULL AND scope = 'tenant';
CREATE UNIQUE INDEX uq_caps_global_model_cap
    ON model_registry_capabilities (model_id, capability)
    WHERE deleted_at IS NULL AND scope = 'global';
CREATE INDEX idx_caps_lookup
    ON model_registry_capabilities (tenant_id, model_id, enabled)
    WHERE deleted_at IS NULL;
COMMENT ON TABLE model_registry_capabilities IS
    'Slice 2: per-model capability rows. capability_params jsonb supports parameterized capabilities (e.g. reasoning_effort {"levels":["high","medium","low"]} per E-NAI-004).';

-- ----------------------------------------------------------------------------
-- ALTER usage_records: add snapshot_version column.
-- Records the registry/router snapshot active at billing time so audit
-- replay can re-derive routing decisions deterministically.
-- Format: "registry:<tenant_id>:<version>;router:<router_policy_version>".
-- Nullable for backwards compatibility with existing rows; new writes populate it.
-- ----------------------------------------------------------------------------
ALTER TABLE usage_records
    ADD COLUMN IF NOT EXISTS snapshot_version text;
COMMENT ON COLUMN usage_records.snapshot_version IS
    'Slice 2: registry+router snapshot stamp at billing time. Format registry:<tid>:<v>;router:<rv>. Audit replay reads this to re-derive what routing config was active.';

COMMIT;
