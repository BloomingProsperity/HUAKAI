-- HUAKAI 配额日志写入。

-- name: InsertQuotaAuditEvent :one
INSERT INTO quota_audit_events (
    tenant_id,
    reservation_id,
    claim_id,
    event_type,
    decision_code,
    scope_kind,
    scope_id,
    metric,
    amount_reserved,
    amount_settled,
    retry_after_seconds,
    payload,
    actor
)
SELECT
    sqlc.arg(tenant_id)::bigint,
    sqlc.narg(reservation_id)::bigint,
    sqlc.narg(claim_id)::bigint,
    sqlc.arg(event_type)::text,
    sqlc.arg(decision_code)::text,
    sqlc.arg(scope_kind)::text,
    sqlc.arg(scope_id)::text,
    sqlc.arg(metric)::text,
    sqlc.arg(amount_reserved)::numeric(20,8),
    sqlc.arg(amount_settled)::numeric(20,8),
    sqlc.narg(retry_after_seconds)::integer,
    sqlc.arg(payload)::jsonb,
    sqlc.narg(actor)::text
WHERE EXISTS (
    SELECT 1
    FROM tenants t
    WHERE t.id = sqlc.arg(tenant_id)::bigint
)
RETURNING
    tenant_id,
    id,
    occurred_at;
