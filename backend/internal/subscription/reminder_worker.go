// HUAKAI · iKun

package subscription

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// ReminderWorker 后台 ticker: 周期扫描临近到期订阅并发分档提醒。
// 单 goroutine; 接 context cancellation 优雅退出。提醒去重由账本保证, 与到期 worker 独立 (不同周期)。
type ReminderWorker struct {
	svc       *ReminderService
	interval  time.Duration
	batchSize int

	mu      sync.Mutex
	started bool
	stop    chan struct{}
	done    chan struct{}

	running     atomic.Bool   // 防 tick 重入
	tickCount   atomic.Uint64 // 累计 tick
	sentTotal   atomic.Uint64 // 累计已发提醒条数
	failedTicks atomic.Uint64 // 出错 tick 数
}

// ReminderWorkerConfig 构造参数。
type ReminderWorkerConfig struct {
	Service   *ReminderService
	Interval  time.Duration // 0 用 DefaultReminderInterval
	BatchSize int           // <=0 用 DefaultReminderBatchSize
}

// NewReminderWorker 构造提醒 worker。
func NewReminderWorker(cfg ReminderWorkerConfig) *ReminderWorker {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultReminderInterval
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultReminderBatchSize
	}
	return &ReminderWorker{
		svc:       cfg.Service,
		interval:  cfg.Interval,
		batchSize: cfg.BatchSize,
	}
}

// Start 启动 ticker goroutine。重复 Start no-op; Stop 后可再 Start。
func (w *ReminderWorker) Start(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started || w.svc == nil {
		return
	}
	w.stop = make(chan struct{})
	w.done = make(chan struct{})
	w.started = true
	go w.loop(ctx)
}

func (w *ReminderWorker) loop(ctx context.Context) {
	defer close(w.done)
	t := time.NewTicker(w.interval)
	defer t.Stop()
	w.tick(ctx) // 启动即跑一次
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case <-t.C:
			w.tick(ctx)
		}
	}
}

// tick 单次提醒扫描。窗口翻页在 ProcessDueReminders 内部用游标完成 (一次 tick 翻完整个窗口),
// 故这里只调一次。running-guard 防同进程并发重入。
func (w *ReminderWorker) tick(ctx context.Context) {
	if !w.running.CompareAndSwap(false, true) {
		return
	}
	defer w.running.Store(false)
	w.tickCount.Add(1)
	n, err := w.svc.ProcessDueReminders(ctx, w.batchSize)
	if n > 0 {
		w.sentTotal.Add(uint64(n))
	}
	if err != nil {
		w.failedTicks.Add(1)
	}
}

// Stop 优雅停止并等 loop 退出。多次 Stop no-op。
func (w *ReminderWorker) Stop() {
	w.mu.Lock()
	if !w.started {
		w.mu.Unlock()
		return
	}
	close(w.stop)
	w.started = false
	doneChan := w.done
	w.mu.Unlock()
	if doneChan != nil {
		<-doneChan
	}
}

// TickOnce 测试用: 同步触发一次扫描。
func (w *ReminderWorker) TickOnce(ctx context.Context) {
	w.tick(ctx)
}

// TickCount 累计 tick 次数。
func (w *ReminderWorker) TickCount() uint64 { return w.tickCount.Load() }

// SentTotal 累计已发提醒条数。
func (w *ReminderWorker) SentTotal() uint64 { return w.sentTotal.Load() }

// FailedTicks 出错 tick 数。
func (w *ReminderWorker) FailedTicks() uint64 { return w.failedTicks.Load() }
