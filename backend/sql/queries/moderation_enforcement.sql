-- 内容审核 30 天运营日志与租户证据读取。
-- 所有查询都显式绑定 tenant_id；永久表不保存正文、摘录或载荷摘要。

-- name: InsertModerationLog :one
INSERT INTO moderation_log (
    tenant_id, api_key_id, user_id, request_id, violation_event_id,
    input_excerpt, decision, reason_code, matched_keyword_id, matched_hash_id,
    violation_count, threshold_reached, key_disabled,
    actor_id, actor_role, violation_fee_usd, billing_event_id
) VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(api_key_id)::bigint,
    sqlc.arg(user_id)::bigint,
    sqlc.narg(request_id)::text,
    sqlc.narg(violation_event_id)::bigint,
    sqlc.arg(input_excerpt)::text,
    sqlc.arg(decision)::text,
    sqlc.arg(reason_code)::text,
    sqlc.narg(matched_keyword_id)::bigint,
    sqlc.narg(matched_hash_id)::bigint,
    sqlc.arg(violation_count)::bigint,
    sqlc.arg(threshold_reached)::boolean,
    sqlc.arg(key_disabled)::boolean,
    sqlc.narg(actor_id)::text,
    sqlc.narg(actor_role)::text,
    0,
    NULL
)
RETURNING id;

-- name: ListModerationLog :many
SELECT id, tenant_id, api_key_id, user_id, request_id, violation_event_id,
       input_excerpt, decision, reason_code, matched_keyword_id, matched_hash_id,
       violation_count, threshold_reached, key_disabled, actor_id, actor_role,
       occurred_at
FROM moderation_log
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND (
    sqlc.narg(api_key_id)::bigint IS NULL
    OR api_key_id = sqlc.narg(api_key_id)::bigint
  )
ORDER BY occurred_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::integer
OFFSET sqlc.arg(page_offset)::integer;

-- name: ListModerationViolations :many
SELECT v.id, v.tenant_id, v.api_key_id, v.user_id, v.request_id,
       v.decision, v.reason_code, v.matched_keyword_id, v.matched_hash_id,
       v.ban_threshold_snapshot, v.ban_window_seconds_snapshot,
       v.violation_count, v.threshold_reached, v.auto_disable_enabled,
       v.disposition_source, v.disposition_result, v.occurred_at,
       COALESCE(l.input_excerpt, '')::text AS input_excerpt,
       COALESCE(l.key_disabled, false)::boolean AS key_disabled
FROM moderation_violation_events v
LEFT JOIN LATERAL (
    SELECT ml.input_excerpt, ml.key_disabled
    FROM moderation_log ml
    WHERE ml.tenant_id = v.tenant_id
      AND ml.violation_event_id = v.id
    ORDER BY ml.id DESC
    LIMIT 1
) l ON true
WHERE v.tenant_id = sqlc.arg(tenant_id)::bigint
  AND (
    sqlc.narg(api_key_id)::bigint IS NULL
    OR v.api_key_id = sqlc.narg(api_key_id)::bigint
  )
  AND (
    sqlc.narg(user_id)::bigint IS NULL
    OR v.user_id = sqlc.narg(user_id)::bigint
  )
ORDER BY v.occurred_at DESC, v.id DESC
LIMIT sqlc.arg(page_limit)::integer
OFFSET sqlc.arg(page_offset)::integer;
