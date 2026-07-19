// Package workerlease 提供多副本后台任务的 PostgreSQL 会话级互斥租约。
package workerlease

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresLease 在一条专用池连接上持有 advisory lock，直到本轮任务释放。
type PostgresLease struct {
	pool      *pgxpool.Pool
	key       int64
	component string
}

// Session 是一个已经取得互斥权的会话租约。只要 Healthy 成功，同一条
// PostgreSQL 会话仍然存活，建立在该会话上的 advisory lock 就仍然有效。
type Session interface {
	Healthy(context.Context) error
	Release()
}

// SessionProvider 为需要跨多个周期保持唯一 leader 的 worker 提供窄接口。
type SessionProvider interface {
	TryAcquireSession(context.Context) (bool, Session, error)
}

type postgresSession struct {
	conn      *pgxpool.Conn
	key       int64
	component string
	once      sync.Once
}

func NewPostgres(pool *pgxpool.Pool, key int64, component string) *PostgresLease {
	return &PostgresLease{pool: pool, key: key, component: component}
}

func (l *PostgresLease) TryAcquire(ctx context.Context) (bool, func(), error) {
	acquired, session, err := l.TryAcquireSession(ctx)
	if err != nil || !acquired {
		return acquired, nil, err
	}
	return true, session.Release, nil
}

// TryAcquireSession 在专用连接上取得会话级租约。调用方必须 Release；连接
// 失效后 Healthy 会报错，worker 应立即停止副作用并重新参与选主。
func (l *PostgresLease) TryAcquireSession(ctx context.Context) (bool, Session, error) {
	if l == nil || l.pool == nil {
		return false, nil, errors.New("worker lease: postgres pool is required")
	}
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return false, nil, fmt.Errorf("worker lease %s: acquire connection: %w", l.component, err)
	}
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, l.key).Scan(&acquired); err != nil {
		conn.Release()
		return false, nil, fmt.Errorf("worker lease %s: acquire lock: %w", l.component, err)
	}
	if !acquired {
		conn.Release()
		return false, nil, nil
	}

	return true, &postgresSession{conn: conn, key: l.key, component: l.component}, nil
}

func (s *postgresSession) Healthy(ctx context.Context) error {
	if s == nil || s.conn == nil {
		return errors.New("worker lease session is unavailable")
	}
	var one int
	if err := s.conn.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil {
		return fmt.Errorf("worker lease %s: session health: %w", s.component, err)
	}
	if one != 1 {
		return fmt.Errorf("worker lease %s: invalid session health result", s.component)
	}
	return nil
}

func (s *postgresSession) Release() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if s.conn == nil {
			return
		}
		releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = s.conn.Exec(releaseCtx, `SELECT pg_advisory_unlock($1)`, s.key)
		s.conn.Release()
	})
}
