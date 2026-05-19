-- Slice 2 (N+5a) Model Registry queries.
-- Per docs/process/plans/2026-04-30-n5-model-registry.md.
-- Per CMB-7: SELECT-only at request time. Snapshot version increments
-- happen via a future Phase E admin writer outside this package.
-- Per CMB-1: NEVER select credentials; this package never joins
-- provider_accounts.credentials, OAuth tokens, or api_keys.key_hash.

-- name: LookupTenantAlias :one
-- Step 1 of resolve. Returns the tenant-scoped alias row regardless of
-- status (active/disabled/deleted-protected). The Go resolver checks
-- status: tenant disabled is an EXPLICIT DENY that blocks global fallback
-- per D3 invariant (integration test #5).
SELECT
    a.id                    AS alias_id,
    a.model_id,
    a.status                AS alias_status,
    a.disabled_reason,
    a.public_alias_display
FROM model_aliases a
WHERE a.tenant_id = sqlc.arg(tenant_id)::bigint
  AND a.public_alias_normalized = sqlc.arg(alias_lower)::text
  AND a.scope = 'tenant'
  AND a.deleted_at IS NULL
LIMIT 1;

-- name: LookupGlobalAlias :one
-- Step 2 of resolve. Only called when tenant lookup misses AND the tenant
-- policy permits global inheritance.
SELECT
    a.id                    AS alias_id,
    a.model_id,
    a.status                AS alias_status,
    a.disabled_reason,
    a.public_alias_display
FROM model_aliases a
WHERE a.scope = 'global'
  AND a.tenant_id IS NULL
  AND a.public_alias_normalized = sqlc.arg(alias_lower)::text
  AND a.deleted_at IS NULL
LIMIT 1;

-- name: GetTenantInheritGlobal :one
-- Returns whether a tenant has opted into global-catalog inheritance.
-- Missing row -> nothing returned -> resolver treats as false.
SELECT inherit_global_catalog
FROM model_registry_tenant_policies
WHERE tenant_id = sqlc.arg(tenant_id)::bigint;

-- name: GetModelByID :one
-- Resolves the canonical model row, constrained to the requesting tenant
-- (scope='tenant' AND tenant_id=$tenant) OR scope='global'. This blocks
-- a misconfigured tenant alias from reaching another tenant's model row
-- (codex N+5a P3 finding 2026-04-30 — defense in depth in addition to
-- admin-write-time validation).
SELECT
    id,
    tenant_id,
    scope,
    canonical_id,
    protocol_family,
    default_provider_model_id,
    default_context_window,
    default_request_timeout_ms,
    pricing_class,
    model_owner,
    status
FROM models
WHERE id = sqlc.arg(id)::bigint
  AND deleted_at IS NULL
  AND (
        (scope = 'tenant' AND tenant_id = sqlc.arg(tenant_id)::bigint)
        OR (scope = 'global' AND tenant_id IS NULL)
      );

-- name: GetTenantSnapshotVersion :one
-- Returns the per-tenant registry version stamp. Missing row means the
-- tenant has no admin writes yet; resolver treats as version 1.
SELECT version
FROM model_registry_snapshots
WHERE tenant_id = sqlc.arg(tenant_id)::bigint;

-- name: ListModelCapabilities :many
-- Returns capability rows visible to (tenant_id, model_id), including
-- global capabilities for the same model. enabled rows only.
SELECT
    capability,
    capability_value,
    capability_params,
    source
FROM model_registry_capabilities
WHERE (
        (tenant_id = sqlc.arg(tenant_id)::bigint AND scope = 'tenant')
        OR (tenant_id IS NULL AND scope = 'global')
      )
  AND model_id = sqlc.arg(model_id)::bigint
  AND deleted_at IS NULL
  AND enabled = true
ORDER BY capability;

-- name: ListModelPoolBindings :many
-- Returns enabled bindings ordered by priority then id, filtered by the
-- effective_from/until time window. ALWAYS tenant-scoped: pool_groups
-- are tenant-owned so a global binding would leak pool_group ids across
-- tenants (codex N+5a P1 finding 2026-04-30 — addressed by removing the
-- scope column from model_pool_bindings entirely; bindings are inherently
-- tenant-local even for global models). Slice 2 emits all candidates;
-- Router selects index 0 only at L0 (AttemptBudget=1).
SELECT
    mpb.id,
    mpb.pool_group_id,
    mpb.priority,
    mpb.weight,
    mpb.selection_mode,
    mpb.provider_model_id_override,
    mpb.rpm_limit,
    mpb.tpm_limit,
    mpb.max_parallel_requests,
    mpb.fallback_class,
    mpb.reason
FROM model_pool_bindings mpb
INNER JOIN pool_groups pg
    ON pg.id = mpb.pool_group_id
   AND pg.tenant_id = mpb.tenant_id
   AND pg.enabled = true
   AND pg.deleted_at IS NULL
WHERE mpb.tenant_id = sqlc.arg(tenant_id)::bigint
  AND mpb.model_id = sqlc.arg(model_id)::bigint
  AND mpb.deleted_at IS NULL
  AND mpb.enabled = true
  AND (mpb.effective_from IS NULL OR mpb.effective_from <= now())
  AND (mpb.effective_until IS NULL OR mpb.effective_until > now())
ORDER BY mpb.priority ASC, mpb.id ASC;
