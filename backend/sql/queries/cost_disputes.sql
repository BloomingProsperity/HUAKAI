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
SELECT cd.id, cd.dispute_id, cd.tenant_id, cd.user_id, cd.request_id, cd.reason, cd.status,
       cd.operator_note, cd.created_at, cd.resolved_at,
       COALESCE(refunds.refunded_micro_usd, 0)::bigint AS refunded_micro_usd
FROM cost_disputes AS cd
LEFT JOIN (
    SELECT tenant_id,
           audit_request_id,
           SUM(ROUND(-actual_cost_signed * 1000000))::bigint AS refunded_micro_usd
    FROM billing_events
    WHERE event_type = 'reconciliation_appended'
      AND actual_cost_signed < 0
    GROUP BY tenant_id, audit_request_id
) AS refunds
  ON refunds.tenant_id = cd.tenant_id
 AND refunds.audit_request_id = 'dispute-' || cd.dispute_id
WHERE cd.tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.arg(status_filter)::text = '' OR cd.status = sqlc.arg(status_filter)::text)
ORDER BY cd.created_at DESC, cd.id DESC
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
  AND status IN ('open', 'reviewing')
RETURNING id, dispute_id, tenant_id, user_id, request_id, reason, status,
          operator_note, created_at, resolved_at;
