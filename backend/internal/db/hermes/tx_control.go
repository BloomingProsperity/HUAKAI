package hermes

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// Exec 把事务控制语句暴露给上层 Hermes store，而无需放宽生成的 DBTX 字段。
func (q *Queries) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	if q == nil || q.db == nil {
		return pgconn.CommandTag{}, errors.New("hermes queries are not configured")
	}
	return q.db.Exec(ctx, sql, args...)
}
