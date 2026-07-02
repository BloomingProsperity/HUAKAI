package quota

import (
	"context"
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

	mu      sync.Mutex
	running bool
	stop    chan struct{}
	done    chan struct{}
	now     func() time.Time
}

// NewReconciliationWorker 构造 worker。interval<=0 用默认分钟级。
func NewReconciliationWorker(reconciler *Reconciler, interval time.Duration) *ReconciliationWorker {
	if interval <= 0 {
		interval = defaultReconciliationWorkerInterval
	}
	return &ReconciliationWorker{
		reconciler: reconciler,
		interval:   interval,
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
			_, _ = w.RunOnce(ctx, w.now())
		}
	}
}

// RunOnce 跑一轮全局 sweep;now 为零值时取当前时间。错误仅返回不致命(下轮重试),
// 供测试直接驱动一轮而不启 goroutine。
func (w *ReconciliationWorker) RunOnce(ctx context.Context, now time.Time) (int, error) {
	if w == nil || w.reconciler == nil {
		return 0, nil
	}
	if now.IsZero() {
		now = w.now()
	}
	return w.reconciler.ReconcileAllTenants(ctx, now.UTC())
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
}
