// 本包承载手工维护的 billing 表卫生/维护查询(幂等重放过期清理、调度 outbox 修剪),
// 独立于 sqlc 再生成;自 internal/db/billing 拆出以按职责分域。
package billingmaint

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
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

const deleteExpiredIdempotencyReplayRecords = `-- name: DeleteExpiredIdempotencyReplayRecords :execrows
DELETE FROM idempotency_replay_records
WHERE expires_at <= now()
`

// 过期清理扫描 (供后台 janitor 周期调用)。
func (q *Queries) DeleteExpiredIdempotencyReplayRecords(ctx context.Context) (int64, error) {
	result, err := q.db.Exec(ctx, deleteExpiredIdempotencyReplayRecords)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

const getIdempotencyReplayRecord = `-- name: GetIdempotencyReplayRecord :one
SELECT response_status, content_type, response_body
FROM idempotency_replay_records
WHERE tenant_id = $1 AND claim_id = $2 AND expires_at > now()
`

type GetIdempotencyReplayRecordParams struct {
	TenantID int64 `db:"tenant_id" json:"tenant_id"`
	ClaimID  int64 `db:"claim_id" json:"claim_id"`
}

type GetIdempotencyReplayRecordRow struct {
	ResponseStatus int32  `db:"response_status" json:"response_status"`
	ContentType    string `db:"content_type" json:"content_type"`
	ResponseBody   []byte `db:"response_body" json:"response_body"`
}

// 取未过期的重放记录; 过期记录视为不存在。
func (q *Queries) GetIdempotencyReplayRecord(ctx context.Context, arg GetIdempotencyReplayRecordParams) (GetIdempotencyReplayRecordRow, error) {
	row := q.db.QueryRow(ctx, getIdempotencyReplayRecord, arg.TenantID, arg.ClaimID)
	var i GetIdempotencyReplayRecordRow
	err := row.Scan(&i.ResponseStatus, &i.ContentType, &i.ResponseBody)
	return i, err
}

const insertIdempotencyReplayRecord = `-- name: InsertIdempotencyReplayRecord :exec

INSERT INTO idempotency_replay_records (
    tenant_id, claim_id, response_status, content_type, response_body, expires_at
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (tenant_id, claim_id) DO NOTHING
`

type InsertIdempotencyReplayRecordParams struct {
	TenantID       int64              `db:"tenant_id" json:"tenant_id"`
	ClaimID        int64              `db:"claim_id" json:"claim_id"`
	ResponseStatus int32              `db:"response_status" json:"response_status"`
	ContentType    string             `db:"content_type" json:"content_type"`
	ResponseBody   []byte             `db:"response_body" json:"response_body"`
	ExpiresAt      pgtype.Timestamptz `db:"expires_at" json:"expires_at"`
}

// 持久幂等重放记录。 请求成功完成后存原始响应体, 供同 Idempotency-Key 重试 (claim 已
// committed → IdempotencyHit) 时路由无关地重放。 ON CONFLICT DO NOTHING: 重放路径本身
// 不应再写, 并发亦去重。 表见 migration 0044。
func (q *Queries) InsertIdempotencyReplayRecord(ctx context.Context, arg InsertIdempotencyReplayRecordParams) error {
	_, err := q.db.Exec(ctx, insertIdempotencyReplayRecord,
		arg.TenantID,
		arg.ClaimID,
		arg.ResponseStatus,
		arg.ContentType,
		arg.ResponseBody,
		arg.ExpiresAt,
	)
	return err
}
