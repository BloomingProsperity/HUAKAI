-- Content moderation sqlc queries.
-- moderation_log writes metadata and payload_hash only; raw request
-- bodies, plaintext credentials, and key hashes never appear in this file.

-- name: ListEnabledModerationKeywords :many
SELECT id, tenant_id, keyword, reason_code, enabled, created_at, updated_at
FROM moderation_keywords
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND enabled = true
  AND deleted_at IS NULL
ORDER BY id ASC;

-- name: ListModerationKeywords :many
SELECT id, tenant_id, keyword, reason_code, enabled, created_at, updated_at
FROM moderation_keywords
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::integer
OFFSET sqlc.arg(page_offset)::integer;

-- name: CreateModerationKeyword :one
INSERT INTO moderation_keywords (
    tenant_id, keyword, reason_code, enabled, created_by, updated_by
) VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(keyword)::text,
    sqlc.arg(reason_code)::text,
    sqlc.arg(enabled)::boolean,
    sqlc.narg(updated_by)::text,
    sqlc.narg(updated_by)::text
)
RETURNING id, tenant_id, keyword, reason_code, enabled, created_at, updated_at;

-- name: SoftDeleteModerationKeyword :execrows
UPDATE moderation_keywords
SET enabled = false,
    deleted_at = now(),
    updated_at = now()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(id)::bigint
  AND deleted_at IS NULL;

-- name: ListModerationHashes :many
SELECT id, tenant_id, hash_hex, reason_code, enabled, created_at, updated_at
FROM moderation_hashes
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::integer
OFFSET sqlc.arg(page_offset)::integer;

-- name: CreateModerationHash :one
INSERT INTO moderation_hashes (
    tenant_id, hash_hex, reason_code, enabled, created_by, updated_by
) VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(hash_hex)::text,
    sqlc.arg(reason_code)::text,
    sqlc.arg(enabled)::boolean,
    sqlc.narg(updated_by)::text,
    sqlc.narg(updated_by)::text
)
RETURNING id, tenant_id, hash_hex, reason_code, enabled, created_at, updated_at;

-- name: SoftDeleteModerationHash :execrows
UPDATE moderation_hashes
SET enabled = false,
    deleted_at = now(),
    updated_at = now()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(id)::bigint
  AND deleted_at IS NULL;

-- name: FindEnabledModerationHash :one
SELECT id, tenant_id, hash_hex, reason_code, enabled, created_at, updated_at
FROM moderation_hashes
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND hash_hex = sqlc.arg(hash_hex)::text
  AND enabled = true
  AND deleted_at IS NULL
LIMIT 1;

-- name: GetModerationConfig :one
SELECT tenant_id, enabled, fail_closed, sample_rate_pct, ban_threshold,
       ban_window_seconds, violation_fee_usd, updated_by, updated_at
FROM moderation_config
WHERE tenant_id = sqlc.arg(tenant_id)::bigint;

-- name: UpsertModerationConfig :one
INSERT INTO moderation_config (
    tenant_id, enabled, fail_closed, sample_rate_pct,
    ban_threshold, ban_window_seconds, violation_fee_usd, updated_by
) VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(enabled)::boolean,
    sqlc.arg(fail_closed)::boolean,
    sqlc.arg(sample_rate_pct)::integer,
    sqlc.arg(ban_threshold)::integer,
    sqlc.arg(ban_window_seconds)::integer,
    sqlc.arg(violation_fee_usd)::numeric,
    sqlc.narg(updated_by)::text
)
ON CONFLICT (tenant_id) DO UPDATE
SET enabled = EXCLUDED.enabled,
    fail_closed = EXCLUDED.fail_closed,
    sample_rate_pct = EXCLUDED.sample_rate_pct,
    ban_threshold = EXCLUDED.ban_threshold,
    ban_window_seconds = EXCLUDED.ban_window_seconds,
    violation_fee_usd = EXCLUDED.violation_fee_usd,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING tenant_id, enabled, fail_closed, sample_rate_pct, ban_threshold,
          ban_window_seconds, violation_fee_usd, updated_by, updated_at;

-- name: InsertModerationLog :one
INSERT INTO moderation_log (
    tenant_id, api_key_id, user_id, request_id, payload_hash,
    decision, reason_code, matched_keyword_id, matched_hash_id,
    violation_fee_usd, billing_event_id
) VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(api_key_id)::bigint,
    sqlc.arg(user_id)::bigint,
    sqlc.narg(request_id)::text,
    sqlc.arg(payload_hash)::text,
    sqlc.arg(decision)::text,
    sqlc.arg(reason_code)::text,
    sqlc.narg(matched_keyword_id)::bigint,
    sqlc.narg(matched_hash_id)::bigint,
    sqlc.arg(violation_fee_usd)::numeric,
    sqlc.narg(billing_event_id)::bigint
)
RETURNING id;

-- name: InsertModerationViolationEvent :one
INSERT INTO moderation_violation_events (
    tenant_id, api_key_id, user_id, request_id, payload_hash,
    decision, reason_code, matched_keyword_id, matched_hash_id
) VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(api_key_id)::bigint,
    sqlc.arg(user_id)::bigint,
    sqlc.narg(request_id)::text,
    sqlc.arg(payload_hash)::text,
    sqlc.arg(decision)::text,
    sqlc.arg(reason_code)::text,
    sqlc.narg(matched_keyword_id)::bigint,
    sqlc.narg(matched_hash_id)::bigint
)
RETURNING id;

-- name: CountModerationBlocksInWindow :one
SELECT count(*)::bigint
FROM moderation_violation_events
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND api_key_id = sqlc.arg(api_key_id)::bigint
  AND occurred_at >= now() - make_interval(secs => sqlc.arg(window_seconds)::integer);

-- name: DisableModerationAPIKey :execrows
UPDATE api_keys
SET status = 'disabled',
    updated_at = now()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(api_key_id)::bigint
  AND status = 'active'
  AND deleted_at IS NULL;
