-- name: InsertPoolRoutingAuditEvent :exec
INSERT INTO pool_routing_audit_events (
    tenant_id,
    event_type,
    pool_group_id,
    provider_account_id,
    request_id,
    actor_id,
    actor_role,
    reason,
    payload,
    created_at
) VALUES (
    sqlc.arg(tenant_id),
    sqlc.arg(event_type),
    sqlc.narg(pool_group_id),
    sqlc.narg(provider_account_id),
    sqlc.narg(request_id),
    sqlc.narg(actor_id),
    sqlc.narg(actor_role),
    sqlc.narg(reason),
    sqlc.arg(payload),
    COALESCE(sqlc.narg(created_at), NOW())
);
