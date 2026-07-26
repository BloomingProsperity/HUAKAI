-- 内容审核 Key 处置状态、人工禁用与恢复查询。

-- name: ListBannedKeys :many
SELECT ak.id, ak.tenant_id, ak.user_id, ak.name, ak.key_prefix, ak.status,
       ak.created_at, ak.updated_at,
       v.violation_count, v.occurred_at AS last_violation_at,
       s.source, s.reason_code, s.disable_generation
FROM moderation_key_states s
JOIN api_keys ak
  ON ak.tenant_id = s.tenant_id
 AND ak.id = s.api_key_id
JOIN moderation_violation_events v
  ON v.tenant_id = s.tenant_id
 AND v.id = s.trigger_event_id
WHERE s.tenant_id = sqlc.arg(tenant_id)::bigint
  AND s.state = 'disabled'
  AND ak.purpose = 'user'
  AND ak.status = 'disabled'
  AND ak.deleted_at IS NULL
ORDER BY s.updated_at DESC, ak.id DESC
LIMIT sqlc.arg(page_limit)::integer
OFFSET sqlc.arg(page_offset)::integer;

-- name: LockModerationKeyState :one
SELECT s.tenant_id, s.api_key_id, s.state, s.source, s.trigger_event_id,
       s.reason_code, s.actor_id, s.actor_role, s.disable_generation,
       ak.status, ak.status_generation, ak.user_id
FROM moderation_key_states s
JOIN api_keys ak
  ON ak.tenant_id = s.tenant_id
 AND ak.id = s.api_key_id
WHERE s.tenant_id = sqlc.arg(tenant_id)::bigint
  AND s.api_key_id = sqlc.arg(api_key_id)::bigint
  AND s.state = 'disabled'
  AND ak.purpose = 'user'
  AND ak.deleted_at IS NULL
FOR UPDATE OF s, ak;

-- name: LockThresholdModerationViolation :one
SELECT v.id, v.tenant_id, v.api_key_id, v.user_id, v.reason_code,
       v.violation_count, v.threshold_reached,
       ak.status, ak.status_generation
FROM moderation_violation_events v
JOIN api_keys ak
  ON ak.tenant_id = v.tenant_id
 AND ak.id = v.api_key_id
 AND ak.user_id = v.user_id
WHERE v.tenant_id = sqlc.arg(tenant_id)::bigint
  AND v.api_key_id = sqlc.arg(api_key_id)::bigint
  AND v.id = sqlc.arg(violation_event_id)::bigint
  AND v.threshold_reached = true
  AND ak.purpose = 'user'
  AND ak.deleted_at IS NULL
FOR UPDATE OF v, ak;

-- name: SetManualModerationDisposition :execrows
UPDATE moderation_violation_events
SET disposition_source = 'manual',
    disposition_result = 'disabled'
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND api_key_id = sqlc.arg(api_key_id)::bigint
  AND id = sqlc.arg(violation_event_id)::bigint
  AND threshold_reached = true;

-- name: EnableModerationAPIKeyCAS :one
UPDATE api_keys
SET status = 'active',
    updated_at = now()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(api_key_id)::bigint
  AND purpose = 'user'
  AND status = 'disabled'
  AND status_generation = sqlc.arg(expected_generation)::bigint
  AND deleted_at IS NULL
RETURNING status, status_generation, updated_at;

-- name: MarkModerationKeyStateActive :execrows
UPDATE moderation_key_states
SET state = 'active',
    actor_id = sqlc.arg(actor_id)::text,
    actor_role = sqlc.arg(actor_role)::text,
    reason_code = sqlc.arg(reason_code)::text,
    updated_at = now()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND api_key_id = sqlc.arg(api_key_id)::bigint
  AND state = 'disabled'
  AND disable_generation = sqlc.arg(expected_generation)::bigint;
