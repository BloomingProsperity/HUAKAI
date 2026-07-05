package quota

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// defaultReconciliationWorkerInterval 是 worker tick 周期。quota 补偿非金额权威(billing
// ledger 才是),延迟一分钟重放对账目正确性无影响,故取分钟级即可。
const defaultReconciliationWorkerInterval = time.Minute

// ReconciliationWorker 是 quota 补偿器的后台驱动:周期调用跨租户全局 sweep,把
// reservation 结算/释放失败后入队的 job 重放掉。生命周期镜像 billing 的后台 worker
//(Start/Stop 幂等、ctx 或 Stop 任一触发即退出)。
//
// 关键:此前 Reconciler 建了但从未接线(死代码),导致 quota_reconciliation_jobs 永久
// 卡 queued、reservation 卡 reserved;本 worker 是让它真跑起来的接线点,由 wiring 在
// knob 打开时 Start。
type ReconciliationWorker struct {
	reconciler *Reconciler
	interval   time.Duration
	// logger 可注入(测试用收集 handler);nil 语义由构造器兜成 slog.Default()。
	logger *slog.Logger

	mu      sync.Mutex
	running bool
	stop    chan struct{}
	done    chan struct{}
	now     func() time.Time
}

// quotaReconciliationComponent 是本 worker 结构化日志的 component 标识。
const quotaReconciliationComponent = "quota_reconciliation_worker"

// NewReconciliationWorker 构造 worker。interval<=0 用默认分钟级。
func NewReconciliationWorker(reconciler *Reconciler, interval time.Duration) *ReconciliationWorker {
	if interval <= 0 {
		interval = defaultReconciliationWorkerInterval
	}
	return &ReconciliationWorker{
		reconciler: reconciler,
		interval:   interval,
		logger:     slog.Default(),
		now:        func() time.Time { return time.Now().UTC() },
	}
}

// Start 幂等启动后台循环。reconciler 为 nil 时空操作(不 panic)。
func (w *ReconciliationWorker) Start(ctx context.Context) {
	if w == nil || w.reconciler == nil {
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
	w.logger.InfoContext(ctx, "quota reconciliation worker started", "component", quotaReconciliationComponent)
	go w.loop(ctx)
}

func (w *ReconciliationWorker) loop(ctx context.Context) {
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
			replayed, err := w.RunOnce(ctx, w.now())
			w.logRound(ctx, replayed, err)
		}
	}
}

// logRound 聚合一轮全局 sweep 结果(每轮一条,不逐租户逐 job 打):此前计数/错误被静默
// 丢弃,补偿 job 持续重放失败运营无从察觉。空转轮只打 Debug,分钟级周期不用 Info 刷屏。
func (w *ReconciliationWorker) logRound(ctx context.Context, replayed int, err error) {
	// 三分支互斥:单租户失败不阻断其余租户(reconciler 常态返回 replayed>0 且 err≠nil),
	// 该轮只打 Warn(已带 processed),否则同轮双发会让 processed 被双计、"failed" 告警被干扰。
	switch {
	case err != nil:
		w.logger.WarnContext(ctx, "quota reconciliation round failed",
			"component", quotaReconciliationComponent, "processed", replayed, "error", err.Error())
	case replayed > 0:
		w.logger.InfoContext(ctx, "quota reconciliation round replayed jobs",
			"component", quotaReconciliationComponent, "processed", replayed)
	default:
		w.logger.DebugContext(ctx, "quota reconciliation round idle", "component", quotaReconciliationComponent)
	}
}

// RunOnce 跑一轮全局 sweep;now 为零值时取当前时间。错误仅返回不致命(下轮重试),
// 供测试直接驱动一轮而不启 goroutine。一轮 = job 重放 + 过期预留清扫两段:前者补
// 「已入队的补偿」,后者兜「job 从未入队」的崩溃窗口,缺任一都会留永久冻结面。
func (w *ReconciliationWorker) RunOnce(ctx context.Context, now time.Time) (int, error) {
	if w == nil || w.reconciler == nil {
		return 0, nil
	}
	if now.IsZero() {
		now = w.now()
	}
	replayed, jobErr := w.reconciler.ReconcileAllTenants(ctx, now.UTC())
	swept, sweepErr := w.reconciler.SweepStaleReservations(ctx, now.UTC(), 0)
	return replayed + swept, errors.Join(jobErr, sweepErr)
}

// Stop 幂等停止并等待循环退出。
func (w *ReconciliationWorker) Stop() {
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
	w.logger.Info("quota reconciliation worker stopped", "component", quotaReconciliationComponent)
}
