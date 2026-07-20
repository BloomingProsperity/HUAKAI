// Phase C.2 生产适配器：基于 DB 的 pool.SlotManager。
//
// 真实的 Acquire/Release 路径：
//   - Acquire 开启一个 Serializable Tx，在同一个 Tx 内递增
//     provider_accounts.in_flight_count 并插入一行新的
//     pool_slot_acquisitions（status='acquired'）。
//     当 cap_concurrency 溢出时，IncrementInFlightCount 返回 0 行，
//     我们将其映射为 ErrNoSlotAvailable。
//   - Release 返回一个幂等的 ReleaseFunc，调用结算器所用的同一个
//     ReleaseSlotAndDecrementInFlight CTE。调用两次是空操作，因为该 CTE
//     只翻转 status='acquired' 的行，且仅在翻转后才递减。

package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// DefaultLeaseDuration 是 slot 租约的宽限窗口，超过后孤儿 slot 回收
// 才能将其回收。
const DefaultLeaseDuration = 90 * time.Second

// DBSlotManager 把 selector 的 slot acquire/release 接缝绑定到真实的
// pool_slot_acquisitions + provider_accounts 行上。
type DBSlotManager struct {
	pool *pgxpool.Pool
	q    *dbbilling.Queries
}

// NewDBSlotManager 从一个 pgx pool 构造该适配器。pool 是必需的，
// 因为 Acquire 会为「递增+插入」这个原子操作开启自己的 Tx。
func NewDBSlotManager(pool *pgxpool.Pool) *DBSlotManager {
	if pool == nil {
		return &DBSlotManager{}
	}
	return &DBSlotManager{pool: pool, q: dbbilling.New(pool)}
}

// CountActiveBindingAcquisitions 为选号前快速拒绝层读取派生在途数。
// SQL 固定只统计 status='acquired'，终态行不会永久占用容量。
func (m *DBSlotManager) CountActiveBindingAcquisitions(ctx context.Context, bindingID int64) (int64, error) {
	if m == nil || m.q == nil {
		return 0, ErrSlotManagerUnavailable
	}
	count, err := m.q.CountActiveBindingAcquisitions(ctx, bindingID)
	if err != nil {
		return 0, fmt.Errorf("pool: count active binding acquisitions: %w", err)
	}
	return count, nil
}

func (m *DBSlotManager) Acquire(ctx context.Context, account *AccountSnapshot, req SelectionRequest) (*AcquireResult, error) {
	if m == nil || m.pool == nil {
		return nil, ErrSlotManagerUnavailable
	}
	if account == nil {
		return nil, errors.New("pool: nil account snapshot")
	}
	// DR-001 跨表 tenant 一致性: 防 selector 在 req.TenantID 写 claim, 而
	// slot acquire 走 account.TenantID, 两边租户不一致会让 slot row 算到
	// 错租户名下, 后续 ReleaseSlotAndDecrement 跟 settlement 找不到对应。
	if req.TenantID != 0 && account.TenantID != req.TenantID {
		return nil, fmt.Errorf("pool: account tenant=%d ≠ request tenant=%d (cross-tenant slot acquire refused)",
			account.TenantID, req.TenantID)
	}
	return retrySerializableSlotAcquire(ctx, func(ctx context.Context) (*AcquireResult, error) {
		return m.acquireOnce(ctx, account, req)
	})
}

func (m *DBSlotManager) acquireOnce(ctx context.Context, account *AccountSnapshot, req SelectionRequest) (*AcquireResult, error) {
	if req.MaxParallelRequests > 0 {
		if req.BindingID <= 0 {
			return nil, errors.New("pool: binding concurrency limit missing binding id")
		}
		// 会话级锁必须先于 SERIALIZABLE 事务获取。若在事务启动后才等待锁，
		// 等待者可能保留锁前旧快照，看不到前一持有者刚提交的 acquisition。
		// 锁定后再开事务，事务内 COUNT、账号递增与 acquisition 插入共享新快照。
		conn, lockQueries, err := m.acquireBindingLockConnection(ctx, req.BindingID)
		if err != nil {
			return nil, err
		}
		defer releaseBindingLockConnection(conn, lockQueries, req.BindingID)

		tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
		if err != nil {
			return nil, fmt.Errorf("pool: begin acquire tx after binding lock: %w", err)
		}
		return m.acquireInTransaction(ctx, tx, account, req)
	}

	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("pool: begin acquire tx: %w", err)
	}
	return m.acquireInTransaction(ctx, tx, account, req)
}

func (m *DBSlotManager) acquireInTransaction(ctx context.Context, tx pgx.Tx, account *AccountSnapshot, req SelectionRequest) (*AcquireResult, error) {
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := m.q.WithTx(tx)
	if req.MaxParallelRequests > 0 {
		// 外层 COUNT 只是优化；锁后的事务内重读才是账号递增与 acquisition
		// 插入前的权威硬闸。
		active, err := qtx.CountActiveBindingAcquisitions(ctx, req.BindingID)
		if err != nil {
			return nil, fmt.Errorf("pool: count binding concurrency in acquire tx: %w", err)
		}
		if active >= req.MaxParallelRequests {
			return nil, ErrBindingConcurrencyLimited
		}
	}
	rows, err := qtx.IncrementInFlightCount(ctx, dbbilling.IncrementInFlightCountParams{
		ID:       account.ID,
		TenantID: account.TenantID,
	})
	if err != nil {
		return nil, fmt.Errorf("pool: increment in_flight_count: %w", err)
	}
	if rows == 0 {
		return nil, ErrNoSlotAvailable
	}

	token := uuid.New()
	var claimID *int64
	if req.ClaimID != 0 {
		c := req.ClaimID
		claimID = &c
	}
	var bindingID *int64
	if req.BindingID > 0 {
		b := req.BindingID
		bindingID = &b
	}
	if _, err := qtx.InsertSlotAcquisition(ctx, dbbilling.InsertSlotAcquisitionParams{
		TenantID:          account.TenantID,
		ProviderAccountID: account.ID,
		BindingID:         bindingID,
		AcquisitionToken:  token,
		ClaimID:           claimID,
		AttemptSeq:        int32(req.AttemptSeq),
		LeaseExpiresAt: pgtype.Timestamptz{
			Time:  time.Now().Add(DefaultLeaseDuration).UTC(),
			Valid: true,
		},
	}); err != nil {
		return nil, fmt.Errorf("pool: insert slot acquisition: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pool: commit acquire tx: %w", err)
	}

	releaseFn := NewIdempotentRelease(token, m.releaseFunc(token))
	return &AcquireResult{
		AcquisitionToken: token,
		Release:          releaseFn,
	}, nil
}

const bindingLockCleanupTimeout = 2 * time.Second
const bindingLockRetryMin = 5 * time.Millisecond
const bindingLockRetryMax = 100 * time.Millisecond

func (m *DBSlotManager) acquireBindingLockConnection(ctx context.Context, bindingID int64) (*pgxpool.Conn, *dbbilling.Queries, error) {
	delay := bindingLockRetryMin
	for {
		conn, err := m.pool.Acquire(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("pool: acquire binding lock connection: %w", err)
		}
		queries := dbbilling.New(conn)
		locked, err := queries.TryAcquireBindingConcurrencyLock(ctx, bindingID)
		if err != nil {
			discardBindingLockConnection(conn)
			return nil, nil, fmt.Errorf("pool: try lock binding concurrency: %w", err)
		}
		if locked {
			return conn, queries, nil
		}
		conn.Release()

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, nil, fmt.Errorf("pool: wait binding concurrency lock: %w", ctx.Err())
		case <-timer.C:
		}
		if delay < bindingLockRetryMax {
			delay *= 2
			if delay > bindingLockRetryMax {
				delay = bindingLockRetryMax
			}
		}
	}
}

// releaseBindingLockConnection 用脱钩上下文解锁；若无法确认锁已释放，就销毁
// 该物理连接，绝不把仍持有会话级锁的连接放回池中。
func releaseBindingLockConnection(conn *pgxpool.Conn, q *dbbilling.Queries, bindingID int64) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), bindingLockCleanupTimeout)
	defer cancel()
	unlocked, err := q.ReleaseBindingConcurrencyLock(cleanupCtx, bindingID)
	if err != nil || !unlocked {
		discardBindingLockConnection(conn)
		return
	}
	conn.Release()
}

func discardBindingLockConnection(conn *pgxpool.Conn) {
	if conn == nil {
		return
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), bindingLockCleanupTimeout)
	defer cancel()
	raw := conn.Hijack()
	_ = raw.Close(closeCtx)
}

// releaseFunc 返回幂等地回滚 Acquire 的闭包。
// ReleaseSlotAndDecrementInFlight 内部的 CTE 只翻转 status 仍为
// 'acquired' 的 slot，因此并发调用（如结算器 vs selector 清理）
// 不会重复递减。
func (m *DBSlotManager) releaseFunc(token uuid.UUID) ReleaseFunc {
	return func(ctx context.Context) error {
		reason := "selector_release"
		if _, err := m.q.ReleaseSlotAndDecrementInFlight(ctx, dbbilling.ReleaseSlotAndDecrementInFlightParams{
			AcquisitionToken: token,
			ReleaseStatus:    "released_failure",
			ReleaseReason:    &reason,
		}); err != nil {
			return fmt.Errorf("pool: release slot: %w", err)
		}
		return nil
	}
}

var _ SlotManager = (*DBSlotManager)(nil)
var _ BindingConcurrencyReader = (*DBSlotManager)(nil)
