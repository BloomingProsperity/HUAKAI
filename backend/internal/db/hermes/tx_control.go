package hermes

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// Exec exposes transaction-control statements to higher-level Hermes stores without widening the generated DBTX field.
func (q *Queries) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	if q == nil || q.db == nil {
		return pgconn.CommandTag{}, errors.New("hermes queries are not configured")
	}
	return q.db.Exec(ctx, sql, args...)
}
