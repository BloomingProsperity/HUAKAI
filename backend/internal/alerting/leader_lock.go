package alerting

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// alertingLeaderLockKey 是告警评估 tick 使用的固定 Postgres advisory-lock 键。
// 这是一个任意但稳定的常量（"ALRT"），与代码库中其它 advisory lock 区分开。
const alertingLeaderLockKey int64 = 0x414C5254

// PostgresLeaderLock 用 session 级的 Postgres advisory lock 实现 LeaderLock。
// pg_try_advisory_lock 是非阻塞的，因此非 leader 副本会立即返回而不是排队。
// 该锁在整个 tick 期间持有在一条专用的池化连接上，结束后释放。
type PostgresLeaderLock struct {
	pool *pgxpool.Pool
}

// NewPostgresLeaderLock 在网关的 pgx 池之上构造一个 leader lock。
func NewPostgresLeaderLock(pool *pgxpool.Pool) *PostgresLeaderLock {
	return &PostgresLeaderLock{pool: pool}
}

func (l *PostgresLeaderLock) TryAcquire(ctx context.Context) (bool, func(), error) {
	if l == nil || l.pool == nil {
		// 未接线 → 表现为唯一 leader（fail-open），保证告警仍然运行。
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
		// 在一个全新的 context 上尽力解锁，这样即使 tick 被取消也能释放锁；
		// 然后把连接归还给连接池。
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, alertingLeaderLockKey)
		conn.Release()
	}
	return true, release, nil
}

var _ LeaderLock = (*PostgresLeaderLock)(nil)
