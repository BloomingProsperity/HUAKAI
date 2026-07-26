-- 内容审核执行日志、违规计数与用户密钥封禁查询。
-- 日志只保存元数据与载荷指纹，不保存原始请求、明文凭据或密钥哈希。

-- name: InsertModerationLog :one
INSERT INTO moderation_log (
    tenant_id, api_key_id, user_id, request_id, payload_hash,
    decision, reason_code, matched_keyword_id, matched_hash_id,
    violation_fee_usd, billing_event_id, input_excerpt
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
    sqlc.narg(billing_event_id)::bigint,
    sqlc.arg(input_excerpt)::text
)
RETURNING id;

-- name: ListModerationLog :many
SELECT id, tenant_id, api_key_id, user_id, request_id, payload_hash,
       decision, reason_code, matched_keyword_id, matched_hash_id,
       violation_fee_usd, billing_event_id, input_excerpt, occurred_at
FROM moderation_log
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND (
    sqlc.narg(api_key_id)::bigint IS NULL
    OR api_key_id = sqlc.narg(api_key_id)::bigint
  )
ORDER BY occurred_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::integer
OFFSET sqlc.arg(page_offset)::integer;

-- name: InsertModerationViolationEvent :one
INSERT INTO moderation_violation_events (
    tenant_id, api_key_id, user_id, request_id, payload_hash,
    decision, reason_code, matched_keyword_id, matched_hash_id,
    input_excerpt
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
    sqlc.arg(input_excerpt)::text
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
  AND purpose = 'user'
  AND status = 'active'
  AND deleted_at IS NULL;

-- name: ListBannedKeys :many
SELECT ak.id, ak.tenant_id, ak.user_id, ak.name, ak.key_prefix, ak.status,
       ak.created_at, ak.updated_at,
       count(v.id)::bigint AS violation_count,
       max(v.occurred_at)::timestamptz AS last_violation_at
FROM api_keys ak
JOIN moderation_violation_events v
  ON v.tenant_id = ak.tenant_id
 AND v.api_key_id = ak.id
 AND v.occurred_at >= ak.updated_at - interval '1 minute'
 AND v.occurred_at <= ak.updated_at + interval '1 second'
WHERE ak.tenant_id = sqlc.arg(tenant_id)::bigint
  AND ak.purpose = 'user'
  AND ak.status = 'disabled'
  AND ak.deleted_at IS NULL
GROUP BY ak.id, ak.tenant_id, ak.user_id, ak.name, ak.key_prefix, ak.status,
         ak.created_at, ak.updated_at
ORDER BY last_violation_at DESC, ak.id DESC
LIMIT sqlc.arg(page_limit)::integer
OFFSET sqlc.arg(page_offset)::integer;

-- name: EnableModerationAPIKey :one
WITH enabled_key AS (
    UPDATE api_keys ak
    SET status = 'active',
        updated_at = now()
    WHERE ak.tenant_id = sqlc.arg(tenant_id)::bigint
      AND ak.id = sqlc.arg(api_key_id)::bigint
      AND ak.purpose = 'user'
      AND ak.status = 'disabled'
      AND ak.deleted_at IS NULL
      AND EXISTS (
          SELECT 1
          FROM moderation_violation_events v
          WHERE v.tenant_id = ak.tenant_id
            AND v.api_key_id = ak.id
            AND v.occurred_at >= ak.updated_at - interval '1 minute'
            AND v.occurred_at <= ak.updated_at + interval '1 second'
      )
    RETURNING ak.id, ak.tenant_id, ak.user_id, ak.status, ak.updated_at
),
audit_row AS (
    INSERT INTO moderation_log (
        tenant_id, api_key_id, user_id, request_id, payload_hash,
        decision, reason_code, violation_fee_usd
    )
    SELECT tenant_id, id, user_id, sqlc.arg(audit_request_id)::text,
           'admin_unban_no_payload', 'pass', sqlc.arg(reason_code)::text, 0
    FROM enabled_key
    RETURNING id
)
SELECT enabled_key.id AS api_key_id, enabled_key.tenant_id, enabled_key.status,
       enabled_key.updated_at, audit_row.id AS audit_log_id
FROM enabled_key
JOIN audit_row ON true;
