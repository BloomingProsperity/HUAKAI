-- Hermes WAVE H3 tool-call audit ledger queries.
--
-- These mirror the append-only audit shape of hermes.sql / admin_audit.sql: a
-- single INSERT per tool invocation, written inside the same transaction as the
-- tool's execution + the mirrored hermes_audit_events / admin_audit_events rows,
-- so the tool-call trail is atomic with the action (or denial).
--
-- requested_args / result_summary are SANITIZED by the application before this
-- INSERT — only system-diagnostic enums / counts / ids / fingerprints, never
-- prompts / completions / raw bodies / secrets / PII.

-- name: InsertHermesToolCall :one
INSERT INTO hermes_tool_calls (
    tenant_id, actor_user_id, admin_actor_token_id, tool_name,
    requested_args, result_status, result_summary, error_class,
    correlation_id, request_id, called_at, returned_at, dry_run
) VALUES (
    sqlc.arg(tenant_id)::bigint,
    sqlc.arg(actor_user_id)::bigint,
    sqlc.narg(admin_actor_token_id)::bigint,
    sqlc.arg(tool_name)::text,
    sqlc.narg(requested_args)::jsonb,
    sqlc.arg(result_status)::text,
    sqlc.narg(result_summary)::jsonb,
    sqlc.narg(error_class)::text,
    sqlc.narg(correlation_id)::text,
    sqlc.narg(request_id)::text,
    sqlc.arg(called_at)::timestamptz,
    sqlc.narg(returned_at)::timestamptz,
    sqlc.arg(dry_run)::boolean
)
RETURNING id, called_at;

-- name: ListHermesToolCallsByTenant :many
SELECT id, tenant_id, actor_user_id, admin_actor_token_id, tool_name,
       requested_args, result_status, result_summary, error_class,
       correlation_id, request_id, called_at, returned_at, dry_run
FROM hermes_tool_calls
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
ORDER BY called_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int;
