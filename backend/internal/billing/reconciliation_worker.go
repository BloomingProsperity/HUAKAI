package billing

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	PendingReconciliationSourceStreamNoUsageFinalized = "stream_no_usage_finalized"

	defaultPendingReconciliationInterval = time.Minute
	defaultPendingReconciliationGrace    = 5 * time.Minute
	defaultPendingReconciliationBatch    = int32(100)
)

type PendingNoUsageFinalizer interface {
	FinalizePendingNoUsage(ctx context.Context, cutoff time.Time, limit int32, source string, reconciledAt time.Time) (int, error)
}

// pendingReconciliationComponent 是本 worker 结构化日志的 component 标识。
const pendingReconciliationComponent = "billing_pending_reconciliation_worker"

type PendingReconciliationWorker struct {
	finalizer PendingNoUsageFinalizer
	interval  time.Duration
	grace     time.Duration
	batch     int32
	// logger 可注入(测试用收集 handler);nil 语义由构造器兜成 slog.Default()。
	logger *slog.Logger

	mu      sync.Mutex
	running bool
	stop    chan struct{}
	done    chan struct{}
	now     func() time.Time
}

func NewPendingReconciliationWorker(finalizer PendingNoUsageFinalizer, interval, grace time.Duration, batch int32) *PendingReconciliationWorker {
	if interval <= 0 {
		interval = defaultPendingReconciliationInterval
	}
	if grace <= 0 {
		grace = defaultPendingReconciliationGrace
	}
	if batch <= 0 {
		batch = defaultPendingReconciliationBatch
	}
	return &PendingReconciliationWorker{
		finalizer: finalizer,
		interval:  interval,
		grace:     grace,
		batch:     batch,
		logger:    slog.Default(),
		now:       func() time.Time { return time.Now().UTC() },
	}
}

func (w *PendingReconciliationWorker) Start(ctx context.Context) {
	if w == nil || w.finalizer == nil {
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
	w.logger.InfoContext(ctx, "pending reconciliation worker started", "component", pendingReconciliationComponent)
	go w.loop(ctx)
}

func (w *PendingReconciliationWorker) loop(ctx context.Context) {
	defer close(w.done)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case <-ticker.C:
			finalized, err := w.RunOnce(ctx, w.now())
			w.logRound(ctx, finalized, err)
		}
	}
}

// logRound 汇总一轮 pending 补结算:此前计数/错误被静默丢弃,流式无 usage 记录长期
// 补不上运营无从察觉。空转轮只打 Debug,分钟级周期不用 Info 刷屏。
func (w *PendingReconciliationWorker) logRound(ctx context.Context, finalized int, err error) {
	// 三分支互斥(同 lease_sweep):失败轮只打 Warn(已带 processed),保证每轮恰一条。
	switch {
	case err != nil:
		w.logger.WarnContext(ctx, "pending reconciliation round failed",
			"component", pendingReconciliationComponent, "processed", finalized, "error", err.Error())
	case finalized > 0:
		w.logger.InfoContext(ctx, "pending reconciliation round finalized records",
			"component", pendingReconciliationComponent, "processed", finalized)
	default:
		w.logger.DebugContext(ctx, "pending reconciliation round idle", "component", pendingReconciliationComponent)
	}
}

func (w *PendingReconciliationWorker) RunOnce(ctx context.Context, now time.Time) (int, error) {
	if w == nil || w.finalizer == nil {
		return 0, nil
	}
	if now.IsZero() {
		now = w.now()
	}
	now = now.UTC()
	return w.finalizer.FinalizePendingNoUsage(
		ctx,
		now.Add(-w.grace),
		w.batch,
		PendingReconciliationSourceStreamNoUsageFinalized,
		now,
	)
}

func (w *PendingReconciliationWorker) Stop() {
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
	// 等 <-done 后再打,保证"stopped"意味着协程真退了。
	w.logger.Info("pending reconciliation worker stopped", "component", pendingReconciliationComponent)
}

type PostgresPendingReconciliationFinalizer struct {
	pool *pgxpool.Pool
}

func NewPostgresPendingReconciliationFinalizer(pool *pgxpool.Pool) *PostgresPendingReconciliationFinalizer {
	return &PostgresPendingReconciliationFinalizer{pool: pool}
}

func (f *PostgresPendingReconciliationFinalizer) FinalizePendingNoUsage(ctx context.Context, cutoff time.Time, limit int32, source string, reconciledAt time.Time) (int, error) {
	if f == nil || f.pool == nil {
		return 0, ErrPoolNotConfigured
	}
	if limit <= 0 {
		limit = defaultPendingReconciliationBatch
	}
	if source == "" {
		source = PendingReconciliationSourceStreamNoUsageFinalized
	}
	if reconciledAt.IsZero() {
		reconciledAt = time.Now().UTC()
	}
	var finalized int
	if err := f.pool.QueryRow(ctx, finalizePendingNoUsageSQL,
		StreamStatePartial.DBValue(),
		cutoff.UTC(),
		source,
		limit,
		reconciledAt.UTC(),
	).Scan(&finalized); err != nil {
		return 0, fmt.Errorf("billing: finalize pending no-usage reconciliation: %w", err)
	}
	return finalized, nil
}

const finalizePendingNoUsageSQL = `
WITH candidates AS (
	SELECT ur.id, ur.tenant_id
	FROM usage_records ur
	WHERE ur.pending_reconciliation = true
	  AND ur.usage_source = 'inferred'
	  AND ur.stream = true
	  AND ur.stream_state = $1
	  AND ur.delivered_token_count > 0
	  AND ur.tokens_input = 0
	  AND ur.tokens_output = 0
	  AND ur.cache_creation_tokens = 0
	  AND ur.cache_read_tokens = 0
	  AND ur.cache_creation_5m_tokens = 0
	  AND ur.cache_creation_1h_tokens = 0
	  AND ur.image_output_tokens = 0
	  AND ur.actual_cost = 0
	  AND ur.settled_at <= $2
	  AND NOT EXISTS (
		SELECT 1
		FROM usage_record_reconciliation_events re
		WHERE re.tenant_id = ur.tenant_id
		  AND re.original_usage_record_id = ur.id
	  )
	ORDER BY ur.settled_at ASC, ur.id ASC
	LIMIT $4
	FOR UPDATE SKIP LOCKED
), inserted AS (
	INSERT INTO usage_record_reconciliation_events (
		tenant_id,
		original_usage_record_id,
		authoritative_tokens_input,
		authoritative_tokens_output,
		authoritative_cost,
		cost_delta,
		reconciliation_source,
		reconciled_at
	)
	SELECT c.tenant_id, c.id, 0, 0, 0, 0, $3, $5
	FROM candidates c
	WHERE NOT EXISTS (
		SELECT 1
		FROM usage_record_reconciliation_events re
		WHERE re.tenant_id = c.tenant_id
		  AND re.original_usage_record_id = c.id
	)
	RETURNING id
)
SELECT count(*)::int FROM inserted`
