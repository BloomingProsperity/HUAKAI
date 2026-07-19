-- name: InsertProviderAccountRaw :one
INSERT INTO provider_accounts (
    tenant_id,
    provider_id,
    channel_id,
    name,
    account_type,
    enabled,
    expires_at,
    credentials,
    cap_concurrency,
    cap_queue_sticky,
    cap_queue_fallback,
    priority,
    static_weight,
    upstream_cost_ratio,
    probe_model,
    tags,
    extra,
    model_allow_list,
    capability_flags,
    rpm_limit,
    tpm_limit,
    window_cost_limit_cents,
    max_sessions,
    disable_cooling,
    refresh_lead_seconds,
    tls_fingerprint_rotate,
    custom_error_codes_enabled,
    custom_error_codes,
    pool_mode,
    temp_unschedulable_enabled,
    temp_unschedulable_rules,
    proxy_id,
    proxy_group_id,
    created_by_actor,
    last_modified_by_actor
) VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(provider_id)::bigint,
    sqlc.arg(channel_id)::bigint,
    sqlc.arg(name)::text,
    sqlc.arg(account_type)::text,
    COALESCE(sqlc.narg(enabled)::boolean, true),
    sqlc.narg(expires_at)::timestamptz,
    sqlc.arg(credentials)::jsonb,
    COALESCE(sqlc.narg(cap_concurrency)::integer, 4),
    COALESCE(sqlc.narg(cap_queue_sticky)::integer, 2),
    COALESCE(sqlc.narg(cap_queue_fallback)::integer, 8),
    COALESCE(sqlc.narg(priority)::integer, 100),
    COALESCE(sqlc.narg(static_weight)::integer, 1),
    sqlc.narg(upstream_cost_ratio)::double precision,
    NULLIF(BTRIM(sqlc.narg(probe_model)::text), ''),
    COALESCE(sqlc.narg(tags)::text[], ARRAY[]::text[]),
    COALESCE(sqlc.narg(extra)::jsonb, '{}'::jsonb),
    COALESCE(sqlc.narg(model_allow_list)::text[], ARRAY[]::text[]),
    COALESCE(sqlc.narg(capability_flags)::text[], ARRAY[]::text[]),
    COALESCE(sqlc.narg(rpm_limit)::bigint, 0),
    COALESCE(sqlc.narg(tpm_limit)::bigint, 0),
    COALESCE(sqlc.narg(window_cost_limit_cents)::bigint, 0),
    COALESCE(sqlc.narg(max_sessions)::integer, 0),
    COALESCE(sqlc.narg(disable_cooling)::boolean, false),
    sqlc.narg(refresh_lead_seconds)::integer,
    COALESCE(sqlc.narg(tls_fingerprint_rotate)::boolean, false),
    COALESCE(sqlc.narg(custom_error_codes_enabled)::boolean, false),
    COALESCE(sqlc.narg(custom_error_codes)::integer[], ARRAY[]::integer[]),
    COALESCE(sqlc.narg(pool_mode)::boolean, false),
    COALESCE(sqlc.narg(temp_unschedulable_enabled)::boolean, false),
    COALESCE(sqlc.narg(temp_unschedulable_rules)::jsonb, '[]'::jsonb),
    sqlc.narg(proxy_id)::bigint,
    NULLIF(BTRIM(sqlc.narg(proxy_group_id)::text), ''),
    sqlc.narg(actor_id)::text,
    sqlc.narg(actor_id)::text
)
RETURNING id;

-- name: UpdateAdminProviderAccountRaw :one
UPDATE provider_accounts
SET
    enabled = COALESCE(sqlc.narg(enabled)::boolean, enabled),
    priority = COALESCE(sqlc.narg(priority)::integer, priority),
    cap_concurrency = COALESCE(sqlc.narg(cap_concurrency)::integer, cap_concurrency),
    static_weight = COALESCE(sqlc.narg(static_weight)::integer, static_weight),
    upstream_cost_ratio = CASE WHEN sqlc.arg(set_upstream_cost_ratio)::boolean THEN sqlc.narg(upstream_cost_ratio)::double precision ELSE upstream_cost_ratio END,
    rpm_limit = COALESCE(sqlc.narg(rpm_limit)::bigint, rpm_limit),
    tpm_limit = COALESCE(sqlc.narg(tpm_limit)::bigint, tpm_limit),
    window_cost_limit_cents = COALESCE(sqlc.narg(window_cost_limit_cents)::bigint, window_cost_limit_cents),
    max_sessions = COALESCE(sqlc.narg(max_sessions)::integer, max_sessions),
    disable_cooling = COALESCE(sqlc.narg(disable_cooling)::boolean, disable_cooling),
    refresh_lead_seconds = CASE WHEN sqlc.arg(set_refresh_lead_seconds)::boolean THEN sqlc.narg(refresh_lead_seconds)::integer ELSE refresh_lead_seconds END,
    expires_at = CASE WHEN sqlc.arg(set_expires_at)::boolean THEN sqlc.narg(expires_at)::timestamptz ELSE expires_at END,
    tls_fingerprint_rotate = COALESCE(sqlc.narg(tls_fingerprint_rotate)::boolean, tls_fingerprint_rotate),
    probe_model = CASE WHEN sqlc.arg(set_probe_model)::boolean THEN NULLIF(BTRIM(sqlc.narg(probe_model)::text), '') ELSE probe_model END,
    tags = CASE WHEN sqlc.arg(set_tags)::boolean THEN COALESCE(sqlc.narg(tags)::text[], ARRAY[]::text[]) ELSE tags END,
    extra = CASE WHEN sqlc.arg(set_extra)::boolean THEN COALESCE(sqlc.narg(extra)::jsonb, '{}'::jsonb) ELSE extra END,
    model_allow_list = CASE WHEN sqlc.arg(set_model_allow_list)::boolean THEN COALESCE(sqlc.narg(model_allow_list)::text[], ARRAY[]::text[]) ELSE model_allow_list END,
    capability_flags = CASE WHEN sqlc.arg(set_capability_flags)::boolean THEN COALESCE(sqlc.narg(capability_flags)::text[], ARRAY[]::text[]) ELSE capability_flags END,
    custom_error_codes_enabled = COALESCE(sqlc.narg(custom_error_codes_enabled)::boolean, custom_error_codes_enabled),
    custom_error_codes = CASE WHEN sqlc.arg(set_custom_error_codes)::boolean THEN COALESCE(sqlc.narg(custom_error_codes)::integer[], ARRAY[]::integer[]) ELSE custom_error_codes END,
    pool_mode = COALESCE(sqlc.narg(pool_mode)::boolean, pool_mode),
    temp_unschedulable_enabled = COALESCE(sqlc.narg(temp_unschedulable_enabled)::boolean, temp_unschedulable_enabled),
    temp_unschedulable_rules = CASE WHEN sqlc.arg(set_temp_unschedulable_rules)::boolean THEN COALESCE(sqlc.narg(temp_unschedulable_rules)::jsonb, '[]'::jsonb) ELSE temp_unschedulable_rules END,
    proxy_id = CASE WHEN sqlc.arg(set_proxy_id)::boolean THEN sqlc.narg(proxy_id)::bigint ELSE proxy_id END,
    proxy_group_id = CASE WHEN sqlc.arg(set_proxy_group_id)::boolean THEN NULLIF(BTRIM(sqlc.narg(proxy_group_id)::text), '') ELSE proxy_group_id END,
    updated_at = NOW(),
    last_modified_by_actor = sqlc.narg(actor_id)::text
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL
RETURNING
    id,
    tenant_id,
    provider_id,
    channel_id,
    name,
    account_type,
    enabled,
    expires_at,
    rpm_limit,
    tpm_limit,
    window_cost_limit_cents,
    max_sessions,
    disable_cooling,
    refresh_lead_seconds,
    tls_fingerprint_rotate,
    health_state,
    credential_state,
    cap_concurrency,
    in_flight_count,
    priority,
    static_weight,
    upstream_cost_ratio,
    probe_model,
    tags,
    extra,
    last_dispatch_at,
    last_probe_latency_ms,
    last_probe_at,
    model_allow_list,
    capability_flags,
    rate_limited_at,
    rate_limit_reset_at,
    rate_limit_reason,
    overload_until,
    temp_unschedulable_until,
    token_version,
    last_refresh_at,
    last_refresh_outcome,
    oauth_endpoint_health,
    custom_error_codes_enabled,
    custom_error_codes,
    pool_mode,
    temp_unschedulable_enabled,
    temp_unschedulable_rules,
    proxy_id,
    proxy_group_id,
    created_at,
    updated_at;

-- name: UpdateProviderAccountEnabled :exec
UPDATE provider_accounts
SET
    enabled = sqlc.arg(enabled)::boolean,
    updated_at = NOW(),
    last_modified_by_actor = sqlc.narg(actor_id)::text
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL;

-- name: UpdateProviderAccountFingerprintProfile :exec
-- 绑定/解绑 provider account 的 TLS 指纹 profile。profile_id 为 NULL → 解绑回内置默认;
-- 非 NULL → 绑定(DB 触发器 0038 校验 profile 属同租户,跨租户绑定被拒)。
UPDATE provider_accounts
SET
    tls_fingerprint_profile_id = sqlc.narg(profile_id)::bigint,
    updated_at = NOW(),
    last_modified_by_actor = sqlc.narg(actor_id)::text
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL;

-- name: SoftDeleteProviderAccount :exec
UPDATE provider_accounts
SET
    deleted_at = COALESCE(deleted_at, NOW()),
    updated_at = NOW(),
    enabled = false,
    last_modified_by_actor = sqlc.narg(actor_id)::text
WHERE id = sqlc.arg(id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL;
