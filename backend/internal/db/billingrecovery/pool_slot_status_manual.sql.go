package billingrecovery

import (
	"context"

	"github.com/google/uuid"
)

const getPoolSlotStatusByToken = `-- name: GetPoolSlotStatusByToken :one
SELECT status
FROM pool_slot_acquisitions
WHERE acquisition_token = $1::uuid
`

func (q *Queries) GetPoolSlotStatusByToken(ctx context.Context, acquisitionToken uuid.UUID) (string, error) {
	row := q.db.QueryRow(ctx, getPoolSlotStatusByToken, acquisitionToken)
	var status string
	err := row.Scan(&status)
	return status, err
}
