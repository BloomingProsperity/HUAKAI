package settlementreconcile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

const (
	reconcileTickerInterval = 60 * time.Second
	reconcileBatchSize      = int32(100)
	reconcileGracePeriod    = 5 * time.Minute
)

type SettlementReconciler struct {
	pool  *pgxpool.Pool
	q     *dbbilling.Queries
	batch int32
	grace time.Duration

	mu      sync.Mutex
	running bool
	stop    chan struct{}
	done    chan struct{}
}

func NewSettlementReconciler(pool *pgxpool.Pool, batch int32, grace time.Duration) *SettlementReconciler {
	if batch <= 0 {
		batch = reconcileBatchSize
	}
	if grace <= 0 {
		grace = reconcileGracePeriod
	}
	return &SettlementReconciler{
		pool:  pool,
		q:     dbbilling.New(pool),
		batch: batch,
		grace: grace,
	}
}

func (r *SettlementReconciler) Start(ctx context.Context) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pool == nil || r.running {
		return
	}
	r.stop = make(chan struct{})
	r.done = make(chan struct{})
	r.running = true
	go r.loop(ctx)
}

func (r *SettlementReconciler) loop(ctx context.Context) {
	defer close(r.done)
	ticker := time.NewTicker(reconcileTickerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stop:
			return
		case <-ticker.C:
			_, _ = r.reconcileOnce(ctx)
		}
	}
}

func (r *SettlementReconciler) ReconcileOnce(ctx context.Context) (int, error) {
	if r == nil {
		return 0, nil
	}
	return r.reconcileOnce(ctx)
}

func (r *SettlementReconciler) reconcileOnce(ctx context.Context) (int, error) {
	if r == nil || r.pool == nil {
		return 0, nil
	}
	q := r.q
	if q == nil {
		q = dbbilling.New(r.pool)
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("settlementreconcile: begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	qtx := q.WithTx(tx)
	rows, err := qtx.SelectPendingReconciliationForFinalize(ctx, dbbilling.SelectPendingReconciliationForFinalizeParams{
		Grace:     durationInterval(r.grace),
		BatchSize: r.batch,
	})
	if err != nil {
		return 0, fmt.Errorf("settlementreconcile: select pending usage records: %w", err)
	}

	// FINALIZE-AFTER-GRACE:流式上游没有 usage block 时不会事后补权威 usage。
	// 本 worker 只把已过宽限期的 provisional 行确认成最终态,防 pending 堆积。
	// 未来若接入权威 true-up,应在这里通过 settler.RefundInTx 追加差额事件,
	// 不在后台伪造一个不存在的上游查询。
	finalized := 0
	var errs []error
	for _, row := range rows {
		if _, err := tx.Exec(ctx, "SAVEPOINT settlement_reconcile_record"); err != nil {
			errs = append(errs, fmt.Errorf("savepoint usage_record %d: %w", row.ID, err))
			continue
		}
		affected, err := qtx.FinalizePendingReconciliation(ctx, dbbilling.FinalizePendingReconciliationParams{
			ID:       row.ID,
			TenantID: row.TenantID,
		})
		if err != nil {
			_, _ = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT settlement_reconcile_record")
			_, _ = tx.Exec(ctx, "RELEASE SAVEPOINT settlement_reconcile_record")
			errs = append(errs, fmt.Errorf("finalize usage_record %d tenant %d: %w", row.ID, row.TenantID, err))
			continue
		}
		if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT settlement_reconcile_record"); err != nil {
			errs = append(errs, fmt.Errorf("release savepoint usage_record %d: %w", row.ID, err))
			continue
		}
		if affected > 0 {
			finalized++
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return finalized, errors.Join(errors.Join(errs...), fmt.Errorf("settlementreconcile: commit tx: %w", err))
	}
	slog.InfoContext(ctx, "settlement reconciliation batch finalized", "selected", len(rows), "finalized", finalized)
	return finalized, errors.Join(errs...)
}

func (r *SettlementReconciler) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return
	}
	close(r.stop)
	done := r.done
	r.running = false
	r.mu.Unlock()
	if done != nil {
		<-done
	}
}

func durationInterval(d time.Duration) pgtype.Interval {
	if d < 0 {
		d = 0
	}
	return pgtype.Interval{Microseconds: d.Microseconds(), Valid: true}
}
