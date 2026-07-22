package payment

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

const (
	defaultExpireSweepInterval   = time.Minute
	defaultExpireSweepBatchLimit = 200
)

type ExpireSweeperConfig struct {
	Store      Store
	Interval   time.Duration
	BatchLimit int
	Clock      func() time.Time
	Logger     *zap.Logger
}

type ExpireSweeper struct {
	store      Store
	interval   time.Duration
	batchLimit int
	clock      func() time.Time
	logger     *zap.Logger

	mu      sync.Mutex
	running bool
	stop    chan struct{}
	done    chan struct{}
}

func NewExpireSweeper(cfg ExpireSweeperConfig) *ExpireSweeper {
	if cfg.Interval <= 0 {
		cfg.Interval = defaultExpireSweepInterval
	}
	if cfg.BatchLimit <= 0 {
		cfg.BatchLimit = defaultExpireSweepBatchLimit
	}
	if cfg.Clock == nil {
		cfg.Clock = func() time.Time { return time.Now().UTC() }
	}
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}
	return &ExpireSweeper{
		store:      cfg.Store,
		interval:   cfg.Interval,
		batchLimit: cfg.BatchLimit,
		clock:      cfg.Clock,
		logger:     cfg.Logger,
	}
}

func (w *ExpireSweeper) Start(ctx context.Context) {
	if w == nil || w.store == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running {
		return
	}
	w.stop = make(chan struct{})
	w.done = make(chan struct{})
	w.running = true
	go w.loop(ctx)
}

func (w *ExpireSweeper) loop(ctx context.Context) {
	defer close(w.done)
	interval := w.interval
	if interval <= 0 {
		interval = defaultExpireSweepInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case <-ticker.C:
			if _, err := w.RunOnce(ctx, w.now()); err != nil && w.logger != nil {
				w.logger.Warn("payment expire sweep failed", zap.Error(err))
			}
		}
	}
}

func (w *ExpireSweeper) RunOnce(ctx context.Context, now time.Time) (int, error) {
	if w == nil || w.store == nil {
		return 0, nil
	}
	if now.IsZero() {
		now = w.now()
	}
	batchLimit := w.batchLimit
	if batchLimit <= 0 {
		batchLimit = defaultExpireSweepBatchLimit
	}
	expired, err := w.store.ExpireStalePendingOrders(ctx, now.UTC(), batchLimit)
	if err != nil {
		return expired, fmt.Errorf("payment expire sweep: %w", err)
	}
	return expired, nil
}

func (w *ExpireSweeper) now() time.Time {
	if w == nil || w.clock == nil {
		return time.Now().UTC()
	}
	return w.clock().UTC()
}

func (w *ExpireSweeper) Stop() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	close(w.stop)
	w.running = false
	done := w.done
	w.mu.Unlock()
	if done != nil {
		<-done
	}
}

// ExpireStalePendingOrders 在同一事务内标记过期订单并写入操作日志。
func (s *PostgresStore) ExpireStalePendingOrders(ctx context.Context, now time.Time, limit int) (int, error) {
	if s == nil || s.pool == nil {
		return 0, ErrStoreNotConfigured
	}
	if limit <= 0 {
		return 0, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("payment: begin expire stale pending orders: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
UPDATE payment_orders
SET status='expired', updated_at=$1
WHERE id IN (
	SELECT id
	FROM payment_orders
	WHERE status='pending'
	  AND expires_at IS NOT NULL
	  AND expires_at < $1
	ORDER BY id
	LIMIT $2
	FOR UPDATE SKIP LOCKED
)
RETURNING tenant_id, id`, now, limit)
	if err != nil {
		return 0, fmt.Errorf("payment: expire stale pending orders: %w", err)
	}

	expired := make([]auditInsert, 0, limit)
	for rows.Next() {
		var event auditInsert
		if err := rows.Scan(&event.TenantID, &event.OrderID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("payment: scan expired order: %w", err)
		}
		event.EventType = AuditOrderExpired
		event.ActorKind = ActorKindSystem
		event.Now = now
		expired = append(expired, event)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("payment: iterate expired orders: %w", err)
	}

	for _, event := range expired {
		if err := insertAuditTx(ctx, tx, event); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("payment: commit expire stale pending orders: %w", err)
	}
	return len(expired), nil
}
