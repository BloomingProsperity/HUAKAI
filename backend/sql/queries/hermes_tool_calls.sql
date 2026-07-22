-- Hermes 工具调用日志查询。
--
-- 普通改动工具在同一事务内先写工具调用和管理员操作日志，执行成功后再更新结果并共同提交。
-- 含外部副作用或独立事务的工具先写恢复日志，结果明确后由恢复流程补齐两类日志。
--
-- 应用层在写入前清洗 requested_args 和 result_summary，只允许系统诊断枚举、计数、标识符
-- 和指纹，不持久化提示词、模型回复、原始请求体、秘密或个人信息。

-- name: InsertHermesToolCall :one
INSERT INTO hermes_tool_calls (
    operation_id, tenant_id, actor_source, actor_id, actor_role, tool_name,
    requested_args, result_status, result_summary, error_class,
    correlation_id, request_id, called_at, returned_at, dry_run, log_category
) VALUES (
    sqlc.narg(operation_id)::uuid,
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(actor_source)::text,
    sqlc.arg(actor_id)::bigint,
    sqlc.arg(actor_role)::text,
    sqlc.arg(tool_name)::text,
    sqlc.narg(requested_args)::jsonb,
    sqlc.arg(result_status)::text,
    sqlc.narg(result_summary)::jsonb,
    sqlc.narg(error_class)::text,
    sqlc.narg(correlation_id)::text,
    sqlc.narg(request_id)::text,
    sqlc.arg(called_at)::timestamptz,
    sqlc.narg(returned_at)::timestamptz,
    sqlc.arg(dry_run)::boolean,
    sqlc.arg(log_category)::text
)
RETURNING id, called_at;

-- name: UpdateHermesToolCallOutcome :execrows
UPDATE hermes_tool_calls
SET result_status = sqlc.arg(result_status)::text,
    result_summary = sqlc.narg(result_summary)::jsonb,
    error_class = sqlc.narg(error_class)::text,
    returned_at = sqlc.arg(returned_at)::timestamptz,
    log_category = sqlc.arg(log_category)::text
WHERE id = sqlc.arg(id)::bigint
  AND operation_id = sqlc.arg(operation_id)::uuid;

-- name: ListHermesToolCallsByTenant :many
SELECT id, operation_id, tenant_id, actor_source, actor_id, actor_role, tool_name,
       requested_args, result_status, result_summary, error_class,
       correlation_id, request_id, called_at, returned_at, dry_run, log_category
FROM hermes_tool_calls
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
ORDER BY called_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int;
