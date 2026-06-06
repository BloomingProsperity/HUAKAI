-- name: CreateCostDispute :one
INSERT INTO cost_disputes (
    dispute_id,
    tenant_id,
    user_id,
    request_id,
    reason,
    status
) VALUES (
    sqlc.arg(dispute_id),
    sqlc.arg(tenant_id),
    sqlc.arg(user_id),
    sqlc.arg(request_id),
    sqlc.arg(reason),
    'open'
)
RETURNING id, dispute_id, tenant_id, user_id, request_id, reason, status,
          operator_note, created_at, resolved_at;

-- name: ListUserCostDisputes :many
SELECT id, dispute_id, tenant_id, user_id, request_id, reason, status,
       operator_note, created_at, resolved_at
FROM cost_disputes
WHERE tenant_id = sqlc.arg(tenant_id)
  AND user_id = sqlc.arg(user_id)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(limit_rows);

-- name: ListDisputesForAdmin :many
SELECT id, dispute_id, tenant_id, user_id, request_id, reason, status,
       operator_note, created_at, resolved_at
FROM cost_disputes
WHERE tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.arg(status_filter)::text = '' OR status = sqlc.arg(status_filter)::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(limit_rows)
OFFSET sqlc.arg(offset_rows);

-- name: ResolveCostDispute :one
UPDATE cost_disputes
SET status = sqlc.arg(status),
    operator_note = sqlc.arg(operator_note),
    resolved_at = CASE
        WHEN sqlc.arg(status) IN ('resolved', 'rejected') THEN now()
        ELSE NULL
    END
WHERE tenant_id = sqlc.arg(tenant_id)
  AND id = sqlc.arg(id)
RETURNING id, dispute_id, tenant_id, user_id, request_id, reason, status,
          operator_note, created_at, resolved_at;
