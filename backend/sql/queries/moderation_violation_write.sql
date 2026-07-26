-- 内容审核永久违规事实与自动处置写入。
-- 永久表不保存正文、摘录或载荷摘要。

-- name: LockModerationAPIKey :one
SELECT id, tenant_id, user_id, status, status_generation
FROM api_keys
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(api_key_id)::bigint
  AND purpose = 'user'
  AND deleted_at IS NULL
FOR UPDATE;

-- name: InsertModerationViolationEvent :one
INSERT INTO moderation_violation_events (
    tenant_id, api_key_id, user_id, request_id,
    decision, reason_code, matched_keyword_id, matched_hash_id,
    ban_threshold_snapshot, ban_window_seconds_snapshot,
    violation_count, threshold_reached, auto_disable_enabled,
    disposition_source, disposition_result
) VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(api_key_id)::bigint,
    sqlc.arg(user_id)::bigint,
    sqlc.arg(request_id)::text,
    sqlc.arg(decision)::text,
    sqlc.arg(reason_code)::text,
    sqlc.narg(matched_keyword_id)::bigint,
    sqlc.narg(matched_hash_id)::bigint,
    sqlc.arg(ban_threshold_snapshot)::integer,
    sqlc.arg(ban_window_seconds_snapshot)::integer,
    0,
    false,
    sqlc.arg(auto_disable_enabled)::boolean,
    'none',
    'unchanged'
)
ON CONFLICT (tenant_id, api_key_id, request_id) DO NOTHING
RETURNING id;

-- name: GetModerationViolationByRequest :one
SELECT id, tenant_id, api_key_id, user_id, request_id,
       decision, reason_code, matched_keyword_id, matched_hash_id,
       ban_threshold_snapshot, ban_window_seconds_snapshot,
       violation_count, threshold_reached, auto_disable_enabled,
       disposition_source, disposition_result, occurred_at
FROM moderation_violation_events
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND api_key_id = sqlc.arg(api_key_id)::bigint
  AND request_id = sqlc.arg(request_id)::text;

-- name: CountModerationBlocksInWindow :one
SELECT count(*)::bigint
FROM moderation_violation_events
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND api_key_id = sqlc.arg(api_key_id)::bigint
  AND occurred_at >= now() - make_interval(secs => sqlc.arg(window_seconds)::integer);

-- name: DisableModerationAPIKey :one
UPDATE api_keys
SET status = 'disabled',
    updated_at = now()
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(api_key_id)::bigint
  AND purpose = 'user'
  AND status = 'active'
  AND deleted_at IS NULL
RETURNING status_generation, updated_at;

-- name: FinalizeModerationViolationEvent :one
UPDATE moderation_violation_events
SET violation_count = sqlc.arg(violation_count)::bigint,
    threshold_reached = sqlc.arg(threshold_reached)::boolean,
    disposition_source = sqlc.arg(disposition_source)::text,
    disposition_result = sqlc.arg(disposition_result)::text
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id = sqlc.arg(violation_event_id)::bigint
RETURNING id;

-- name: UpsertModerationKeyState :exec
INSERT INTO moderation_key_states (
    tenant_id, api_key_id, state, source, trigger_event_id,
    reason_code, actor_id, actor_role, disable_generation, updated_at
) VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(api_key_id)::bigint,
    'disabled',
    sqlc.arg(source)::text,
    sqlc.arg(trigger_event_id)::bigint,
    sqlc.arg(reason_code)::text,
    sqlc.arg(actor_id)::text,
    sqlc.arg(actor_role)::text,
    sqlc.arg(disable_generation)::bigint,
    now()
)
ON CONFLICT (tenant_id, api_key_id) DO UPDATE
SET state = 'disabled',
    source = EXCLUDED.source,
    trigger_event_id = EXCLUDED.trigger_event_id,
    reason_code = EXCLUDED.reason_code,
    actor_id = EXCLUDED.actor_id,
    actor_role = EXCLUDED.actor_role,
    disable_generation = EXCLUDED.disable_generation,
    updated_at = now();
