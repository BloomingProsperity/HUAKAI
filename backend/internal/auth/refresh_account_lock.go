package auth

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const refreshAccountUnlockTimeout = 5 * time.Second

// StormControllerOption 配置账号刷新调度的生产级保护能力。
type StormControllerOption func(*StormController)

// WithRefreshAccountLockPool 启用数据库会话级账号互斥锁。锁覆盖远端刷新全过程，
// 但不持有数据库事务；连接断开时 PostgreSQL 会自动释放锁。
func WithRefreshAccountLockPool(pool *pgxpool.Pool) StormControllerOption {
	return func(c *StormController) {
		c.refreshLockPool = pool
	}
}

func (c *StormController) acquireRefreshAccountLock(ctx context.Context, tenantID, accountID int64) (func(), bool, error) {
	if c == nil || c.refreshLockPool == nil {
		return func() {}, true, nil
	}
	conn, err := c.refreshLockPool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("auth: acquire refresh lock connection: %w", err)
	}
	key := fmt.Sprintf("credential_refresh_account:%d:%d", tenantID, accountID)
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, key).Scan(&acquired); err != nil {
		conn.Release()
		return nil, false, fmt.Errorf("auth: acquire refresh account lock: %w", err)
	}
	if !acquired {
		conn.Release()
		return nil, false, nil
	}

	var once sync.Once
	release := func() {
		once.Do(func() {
			unlockCtx, cancel := context.WithTimeout(context.Background(), refreshAccountUnlockTimeout)
			defer cancel()
			var unlocked bool
			err := conn.QueryRow(unlockCtx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, key).Scan(&unlocked)
			if err == nil && unlocked {
				conn.Release()
				return
			}
			// 会话锁状态不确定时不能把连接放回池；关闭物理连接让 PostgreSQL 兜底释放锁。
			raw := conn.Hijack()
			_ = raw.Close(unlockCtx)
			slog.Warn("auth: refresh account lock release failed; connection closed",
				"tenant_id", tenantID, "account_id", accountID, "unlocked", unlocked, "err", err)
		})
	}
	return release, true, nil
}
