-- User-owned API key control queries.
-- CMB-5: this file must not select bearer credential material.

-- name: UpsertAPIKeyQuotaPolicy :one
INSERT INTO quota_policies (
    tenant_id,
    scope_kind,
    scope_id,
    metric,
    window_kind,
    window_seconds,
    limit_value,
    mode,
    priority,
    enabled,
    valid_from,
    valid_until,
    created_by_actor,
    last_modified_by_actor
)
SELECT
    sqlc.arg(tenant_id)::bigint,
    'api_key',
    sqlc.arg(scope_id)::text,
    'cost_usd',
    sqlc.arg(window_kind)::text,
    sqlc.arg(window_seconds)::integer,
    sqlc.arg(limit_value)::numeric(20,8),
    sqlc.arg(mode)::text,
    200,
    true,
    sqlc.arg(valid_from)::timestamptz,
    NULL,
    sqlc.arg(actor)::text,
    sqlc.arg(actor)::text
WHERE EXISTS (
    SELECT 1
    FROM api_keys ak
    JOIN tenants t
      ON t.id = ak.tenant_id
     AND t.deleted_at IS NULL
     AND t.status = 'active'
    JOIN users u
      ON u.id = ak.user_id
     AND u.tenant_id = ak.tenant_id
     AND u.deleted_at IS NULL
     AND u.status = 'active'
    WHERE ak.id = sqlc.arg(api_key_id)::bigint
      AND ak.tenant_id = sqlc.arg(tenant_id)::bigint
      AND ak.user_id = sqlc.arg(user_id)::bigint
      AND ak.deleted_at IS NULL
)
-- The live uniqueness surface is a partial unique index, so the executable
-- clause names the same indexed columns and predicate directly.
ON CONFLICT (
    tenant_id,
    scope_kind,
    scope_id,
    metric,
    window_kind,
    window_seconds,
    priority
)
WHERE enabled = true AND valid_until IS NULL
DO UPDATE SET
    limit_value = EXCLUDED.limit_value,
    mode = EXCLUDED.mode,
    valid_from = EXCLUDED.valid_from,
    last_modified_by_actor = EXCLUDED.last_modified_by_actor,
    updated_at = NOW()
RETURNING
    sqlc.arg(api_key_id)::bigint AS api_key_id,
    tenant_id,
    id,
    scope_kind,
    scope_id,
    metric,
    window_kind,
    window_seconds,
    limit_value,
    mode,
    priority,
    enabled,
    valid_from,
    valid_until;

-- name: SetAPIKeyQuotaPolicyID :execrows
UPDATE api_keys ak
SET quota_policy_id = sqlc.arg(quota_policy_id)::bigint,
    updated_at = NOW()
WHERE ak.id = sqlc.arg(api_key_id)::bigint
  AND ak.tenant_id = sqlc.arg(tenant_id)::bigint
  AND ak.user_id = sqlc.arg(user_id)::bigint
  AND ak.deleted_at IS NULL;

-- name: SetAPIKeyIPAllowlist :execrows
UPDATE api_keys ak
SET ip_allowlist = sqlc.narg(ip_allowlist)::text,
    updated_at = NOW()
WHERE ak.id = sqlc.arg(api_key_id)::bigint
  AND ak.tenant_id = sqlc.arg(tenant_id)::bigint
  AND ak.user_id = sqlc.arg(user_id)::bigint
  AND ak.deleted_at IS NULL;

-- name: GetAPIKeyIPAllowlist :one
SELECT
    ak.id AS api_key_id,
    ak.ip_allowlist
FROM api_keys ak
WHERE ak.id = sqlc.arg(api_key_id)::bigint
  AND ak.tenant_id = sqlc.arg(tenant_id)::bigint
  AND ak.user_id = sqlc.arg(user_id)::bigint
  AND ak.deleted_at IS NULL;

-- name: GetAPIKeyQuotaPolicy :one
SELECT
    ak.id AS api_key_id,
    qp.tenant_id,
    qp.id,
    qp.scope_kind,
    qp.scope_id,
    qp.metric,
    qp.window_kind,
    qp.window_seconds,
    qp.limit_value,
    qp.mode,
    qp.priority,
    qp.enabled,
    qp.valid_from,
    qp.valid_until
FROM api_keys ak
JOIN quota_policies qp
  ON qp.tenant_id = ak.tenant_id
 AND qp.id = ak.quota_policy_id
WHERE ak.id = sqlc.arg(api_key_id)::bigint
  AND ak.tenant_id = sqlc.arg(tenant_id)::bigint
  AND ak.user_id = sqlc.arg(user_id)::bigint
  AND ak.deleted_at IS NULL
  AND qp.scope_kind = 'api_key'
  AND qp.scope_id = ak.id::text
  AND qp.metric = 'cost_usd'
  AND qp.enabled = true
  AND qp.valid_until IS NULL;

-- name: ValidateGroupBelongsToTenant :one
SELECT
    id,
    name,
    description,
    enabled
FROM api_key_groups
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(group_id)::bigint
  AND deleted_at IS NULL
  AND enabled = true;

-- name: SetAPIKeyGroupID :execrows
UPDATE api_keys ak
SET key_group_id = sqlc.narg(key_group_id)::bigint,
    updated_at = NOW()
WHERE ak.id = sqlc.arg(api_key_id)::bigint
  AND ak.tenant_id = sqlc.arg(tenant_id)::bigint
  AND ak.user_id = sqlc.arg(user_id)::bigint
  AND ak.deleted_at IS NULL;

-- name: GetAPIKeyGroup :one
SELECT
    ak.id AS api_key_id,
    g.id AS key_group_id,
    g.name AS group_name,
    g.description AS group_description,
    g.enabled AS group_enabled
FROM api_keys ak
LEFT JOIN api_key_groups g
  ON g.tenant_id = ak.tenant_id
 AND g.id = ak.key_group_id
 AND g.deleted_at IS NULL
WHERE ak.id = sqlc.arg(api_key_id)::bigint
  AND ak.tenant_id = sqlc.arg(tenant_id)::bigint
  AND ak.user_id = sqlc.arg(user_id)::bigint
  AND ak.deleted_at IS NULL;
