package alerting

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// alertingLeaderLockKey is the fixed Postgres advisory-lock key for the alert
// evaluation tick. Arbitrary stable constant ("ALRT"), distinct from other
// advisory locks in the codebase.
const alertingLeaderLockKey int64 = 0x414C5254

// PostgresLeaderLock implements LeaderLock with a session-scoped Postgres
// advisory lock. pg_try_advisory_lock is non-blocking, so a non-leader replica
// returns immediately instead of queueing. The lock is held on a dedicated
// pooled connection for the duration of the tick and released afterwards.
type PostgresLeaderLock struct {
	pool *pgxpool.Pool
}

// NewPostgresLeaderLock builds a leader lock over the gateway's pgx pool.
func NewPostgresLeaderLock(pool *pgxpool.Pool) *PostgresLeaderLock {
	return &PostgresLeaderLock{pool: pool}
}

func (l *PostgresLeaderLock) TryAcquire(ctx context.Context) (bool, func(), error) {
	if l == nil || l.pool == nil {
		// Not wired → behave as the sole leader (fail-open) so alerting still runs.
		return true, func() {}, nil
	}
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return false, nil, fmt.Errorf("alerting leader-lock: acquire conn: %w", err)
	}
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, alertingLeaderLockKey).Scan(&acquired); err != nil {
		conn.Release()
		return false, nil, fmt.Errorf("alerting leader-lock: try lock: %w", err)
	}
	if !acquired {
		conn.Release()
		return false, nil, nil
	}
	release := func() {
		// Best-effort unlock on a fresh context so a cancelled tick still frees the
		// lock; then return the connection to the pool.
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, alertingLeaderLockKey)
		conn.Release()
	}
	return true, release, nil
}

var _ LeaderLock = (*PostgresLeaderLock)(nil)
