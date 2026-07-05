// 本文件承载手工维护的查询,独立于 sqlc 再生成。
package quota

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
)

const getBillingClaimTerminalState = `-- name: GetBillingClaimTerminalState :one
SELECT status, actual_cost
FROM billing_ledger_claims
WHERE tenant_id = $1::bigint
  AND id = $2::bigint
`

type GetBillingClaimTerminalStateParams struct {
	TenantID int64 `db:"tenant_id" json:"tenant_id"`
	ClaimID  int64 `db:"claim_id" json:"claim_id"`
}

type GetBillingClaimTerminalStateRow struct {
	Status     string              `db:"status" json:"status"`
	ActualCost decimal.NullDecimal `db:"actual_cost" json:"actual_cost"`
}

// claim 终态点查: 补偿动作执行前复核 claim 现状(actual_cost 非 NULL 等价于已 commit 写入实结额)。
func (q *Queries) GetBillingClaimTerminalState(ctx context.Context, arg GetBillingClaimTerminalStateParams) (GetBillingClaimTerminalStateRow, error) {
	row := q.db.QueryRow(ctx, getBillingClaimTerminalState, arg.TenantID, arg.ClaimID)
	var i GetBillingClaimTerminalStateRow
	err := row.Scan(&i.Status, &i.ActualCost)
	return i, err
}

const listStaleReservedQuotaReservations = `-- name: ListStaleReservedQuotaReservations :many
SELECT qr.tenant_id,
       qr.id AS reservation_id,
       qr.claim_id,
       qr.predicted_cost,
       blc.status AS claim_status,
       blc.actual_cost AS claim_actual_cost
FROM quota_reservations qr
JOIN billing_ledger_claims blc
  ON blc.tenant_id = qr.tenant_id
 AND blc.id = qr.claim_id
WHERE qr.status IN ('reserved', 'reconciliation_needed')
  AND qr.lease_expires_at <= $1::timestamptz
  AND blc.status IN ('committed', 'aborted')
  AND NOT EXISTS (
      SELECT 1
      FROM quota_reconciliation_jobs j
      WHERE j.tenant_id = qr.tenant_id
        AND j.claim_id = qr.claim_id
        AND j.status IN ('queued', 'running', 'failed')
  )
ORDER BY qr.lease_expires_at ASC, qr.id ASC
LIMIT $2::integer
`

type ListStaleReservedQuotaReservationsParams struct {
	AtTime   pgtype.Timestamptz `db:"at_time" json:"at_time"`
	RowLimit int32              `db:"row_limit" json:"row_limit"`
}

type ListStaleReservedQuotaReservationsRow struct {
	TenantID        int64               `db:"tenant_id" json:"tenant_id"`
	ReservationID   int64               `db:"reservation_id" json:"reservation_id"`
	ClaimID         int64               `db:"claim_id" json:"claim_id"`
	PredictedCost   pgtype.Numeric      `db:"predicted_cost" json:"predicted_cost"`
	ClaimStatus     string              `db:"claim_status" json:"claim_status"`
	ClaimActualCost decimal.NullDecimal `db:"claim_actual_cost" json:"claim_actual_cost"`
}

// 清扫入口: lease 过期未终态 + claim 已终态 + 无任何补偿 job 史的孤儿预留(有 job 史的行归 job 重放段, 其退避与终态停靠不可被清扫段每轮重试击穿)。
func (q *Queries) ListStaleReservedQuotaReservations(ctx context.Context, arg ListStaleReservedQuotaReservationsParams) ([]ListStaleReservedQuotaReservationsRow, error) {
	rows, err := q.db.Query(ctx, listStaleReservedQuotaReservations, arg.AtTime, arg.RowLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ListStaleReservedQuotaReservationsRow
	for rows.Next() {
		var i ListStaleReservedQuotaReservationsRow
		if err := rows.Scan(
			&i.TenantID,
			&i.ReservationID,
			&i.ClaimID,
			&i.PredictedCost,
			&i.ClaimStatus,
			&i.ClaimActualCost,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
