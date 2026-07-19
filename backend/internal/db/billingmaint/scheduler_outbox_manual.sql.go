// scheduler_outbox 修剪查询:每笔成功结算都会写一行 outbox(缓存失效传播),消费后
// (或长期无消费者时)历史行只剩排障价值,必须周期修剪,否则表随请求量无界增长。
package billingmaint

import (
	"context"
	"time"
)

const pruneSchedulerOutboxRows = `-- name: PruneSchedulerOutboxRows :execrows
DELETE FROM scheduler_outbox
WHERE id IN (
    SELECT id FROM scheduler_outbox
    WHERE created_at <= $1::timestamptz
       OR (consumed_at IS NOT NULL AND consumed_at <= $2::timestamptz)
    ORDER BY id
    LIMIT $3::bigint
)
`

type PruneSchedulerOutboxRowsParams struct {
	CreatedBefore  time.Time `db:"created_before" json:"created_before"`
	ConsumedBefore time.Time `db:"consumed_before" json:"consumed_before"`
	BatchLimit     int64     `db:"batch_limit" json:"batch_limit"`
}

// PruneSchedulerOutboxRows 删除超龄 outbox 行:创建时间早于 created_before 的一律删
// (无论是否已消费——设计上消费延迟以秒计,超龄未消费行已无失效价值);已消费且消费时间
// 早于 consumed_before 的也删(消费后仅短期保留供排障)。LIMIT 批量删,避免首次启用时
// 对历史积压一次性长事务;调用方按返回行数决定是否继续下一批。
func (q *Queries) PruneSchedulerOutboxRows(ctx context.Context, arg PruneSchedulerOutboxRowsParams) (int64, error) {
	result, err := q.db.Exec(ctx, pruneSchedulerOutboxRows, arg.CreatedBefore, arg.ConsumedBefore, arg.BatchLimit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}
