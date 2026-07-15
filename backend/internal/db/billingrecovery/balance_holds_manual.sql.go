// 本文件承载手工维护的 billing 恢复查询，独立于 sqlc 再生成。
package billingrecovery

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type DBTX interface {
	Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error)
	Query(context.Context, string, ...interface{}) (pgx.Rows, error)
	QueryRow(context.Context, string, ...interface{}) pgx.Row
}

type Queries struct {
	db DBTX
}

func New(db DBTX) *Queries {
	return &Queries{db: db}
}

const expediteAbortLease = `-- name: ExpediteAbortLease :execrows
UPDATE billing_ledger_claims
SET lease_expires_at = LEAST(lease_expires_at, NOW())
WHERE tenant_id = $1::bigint
  AND id = $2::bigint
  AND attempt_seq = $3::integer
  AND status = 'reserving'
`

type ExpediteAbortLeaseParams struct {
	TenantID   int64 `db:"tenant_id" json:"tenant_id"`
	ClaimID    int64 `db:"claim_id" json:"claim_id"`
	AttemptSeq int32 `db:"attempt_seq" json:"attempt_seq"`
}

// ExpediteAbortLease 仅缩短同一 attempt 仍未终结 claim 的 lease，不裁决钱账终态。
func (q *Queries) ExpediteAbortLease(ctx context.Context, arg ExpediteAbortLeaseParams) (int64, error) {
	result, err := q.db.Exec(ctx, expediteAbortLease, arg.TenantID, arg.ClaimID, arg.AttemptSeq)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}
