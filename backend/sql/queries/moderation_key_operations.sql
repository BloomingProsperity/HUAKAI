-- 人工禁用与解封的永久幂等事实。

-- name: AcquireModerationKeyOperationLock :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(lock_key)::text, 0));

-- name: GetModerationKeyOperation :one
SELECT id, tenant_id, api_key_id, idempotency_key, request_fingerprint,
       action, violation_event_id, actor_id, actor_role,
       result_status, result_log_id, result_generation,
       result_updated_at, created_at
FROM moderation_key_operations
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND idempotency_key = sqlc.arg(idempotency_key)::text;

-- name: InsertModerationKeyOperation :one
INSERT INTO moderation_key_operations (
    tenant_id, api_key_id, idempotency_key, request_fingerprint,
    action, violation_event_id, actor_id, actor_role,
    result_status, result_log_id, result_generation, result_updated_at
) VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(api_key_id)::bigint,
    sqlc.arg(idempotency_key)::text,
    sqlc.arg(request_fingerprint)::text,
    sqlc.arg(action)::text,
    sqlc.narg(violation_event_id)::bigint,
    sqlc.arg(actor_id)::text,
    sqlc.arg(actor_role)::text,
    sqlc.arg(result_status)::text,
    sqlc.arg(result_log_id)::bigint,
    sqlc.arg(result_generation)::bigint,
    sqlc.arg(result_updated_at)::timestamptz
)
RETURNING id;
