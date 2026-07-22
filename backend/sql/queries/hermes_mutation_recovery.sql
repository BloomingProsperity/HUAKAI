-- 独立事务变更先登记已确认意图，执行后再写结果。恢复任务只处理尚未完成日志提交的记录。

-- name: InsertHermesMutationRecovery :exec
INSERT INTO hermes_mutation_recovery (
    operation_id, tenant_id, actor_source, actor_id, actor_role, tool_name,
    requested_args, admin_action, target_type, target_id, audit_payload,
    correlation_id, request_id, called_at
) VALUES (
    sqlc.arg(operation_id)::uuid,
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(actor_source)::text,
    sqlc.arg(actor_id)::bigint,
    sqlc.arg(actor_role)::text,
    sqlc.arg(tool_name)::text,
    sqlc.arg(requested_args)::jsonb,
    sqlc.arg(admin_action)::text,
    sqlc.arg(target_type)::text,
    sqlc.arg(target_id)::bigint,
    sqlc.arg(audit_payload)::jsonb,
    sqlc.narg(correlation_id)::text,
    sqlc.narg(request_id)::text,
    sqlc.arg(called_at)::timestamptz
);

-- name: SetHermesMutationRecoveryOutcome :execrows
UPDATE hermes_mutation_recovery
SET result_status = sqlc.arg(result_status)::text,
    result_summary = sqlc.narg(result_summary)::jsonb,
    error_class = sqlc.narg(error_class)::text,
    returned_at = sqlc.arg(returned_at)::timestamptz,
    next_recovery_at = clock_timestamp(),
    updated_at = clock_timestamp()
WHERE operation_id = sqlc.arg(operation_id)::uuid
  AND audit_committed_at IS NULL;

-- name: ClaimHermesMutationRecovery :one
WITH candidate AS (
    SELECT operation_id
    FROM hermes_mutation_recovery
    WHERE audit_committed_at IS NULL
      AND next_recovery_at <= clock_timestamp()
      AND (lease_until IS NULL OR lease_until < clock_timestamp())
    ORDER BY next_recovery_at, created_at, operation_id
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
UPDATE hermes_mutation_recovery recovery
SET lease_owner = sqlc.arg(lease_owner)::text,
    lease_until = clock_timestamp() + sqlc.arg(lease_ttl)::interval,
    recovery_attempts = recovery.recovery_attempts + 1,
    last_recovery_at = clock_timestamp(),
    updated_at = clock_timestamp()
FROM candidate
WHERE recovery.operation_id = candidate.operation_id
RETURNING recovery.*;

-- name: SetClaimedHermesMutationRecoveryOutcome :execrows
UPDATE hermes_mutation_recovery
SET result_status = sqlc.arg(result_status)::text,
    result_summary = sqlc.narg(result_summary)::jsonb,
    error_class = sqlc.narg(error_class)::text,
    returned_at = sqlc.arg(returned_at)::timestamptz,
    next_recovery_at = clock_timestamp(),
    updated_at = clock_timestamp()
WHERE operation_id = sqlc.arg(operation_id)::uuid
  AND audit_committed_at IS NULL
  AND lease_owner = sqlc.arg(lease_owner)::text
  AND lease_until >= clock_timestamp();

-- name: ReleaseHermesMutationRecovery :execrows
UPDATE hermes_mutation_recovery
SET lease_owner = NULL,
    lease_until = NULL,
    next_recovery_at = clock_timestamp() + sqlc.arg(retry_after)::interval,
    updated_at = clock_timestamp()
WHERE operation_id = sqlc.arg(operation_id)::uuid
  AND audit_committed_at IS NULL
  AND lease_owner = sqlc.arg(lease_owner)::text;

-- name: GetHermesMutationRecoveryForUpdate :one
SELECT *
FROM hermes_mutation_recovery
WHERE operation_id = sqlc.arg(operation_id)::uuid
FOR UPDATE;

-- name: MarkHermesMutationRecoveryAudited :execrows
UPDATE hermes_mutation_recovery
SET audit_committed_at = clock_timestamp(),
    lease_owner = NULL,
    lease_until = NULL,
    updated_at = clock_timestamp()
WHERE operation_id = sqlc.arg(operation_id)::uuid
  AND audit_committed_at IS NULL
  AND result_status <> 'prepared';
