// HUAKAI · iKun

package subscription

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/workerpulse"
)

// DefaultExpiryInterval 到期扫描周期 (默认 1 分钟)。
const DefaultExpiryInterval = 1 * time.Minute

// DefaultExpiryBatchSize 单次扫描批量上限 (默认 300)。
const DefaultExpiryBatchSize = 300

// ExpiryWorker 后台 ticker: 周期把到点 active 订阅置 expired (关配额 + 降级)。
// 单 goroutine; 接 context cancellation 优雅退出。配额窗口的"周期重置"由 quota 引擎日历窗口
// 自动完成，本 worker 只处理订阅到期，不负责周期重置。
type ExpiryWorker struct {
	svc       *Service
	interval  time.Duration
	batchSize int

	mu      sync.Mutex
	started bool
	stop    chan struct{}
	done    chan struct{}

	running      atomic.Bool   // 防 tick 重入 (TickOnce 与 loop 并发)
	tickCount    atomic.Uint64 // 累计本副本实际执行
	expiredTotal atomic.Uint64 // 累计到期处理条数
	failedTicks  atomic.Uint64 // 出错 tick 数 (运维 metrics)

	leaderLease LeaderLease
	pulse       workerpulse.Recorder
	replicaID   string
}

// ExpiryWorkerConfig 构造参数。
type ExpiryWorkerConfig struct {
	Service     *Service
	Interval    time.Duration // 0 用 DefaultExpiryInterval
	BatchSize   int           // <=0 用 DefaultExpiryBatchSize
	LeaderLease LeaderLease
	Pulse       workerpulse.Recorder
	ReplicaID   string
}

// NewExpiryWorker 构造到期 worker。
func NewExpiryWorker(cfg ExpiryWorkerConfig) *ExpiryWorker {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultExpiryInterval
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultExpiryBatchSize
	}
	replicaID := cfg.ReplicaID
	if replicaID == "" {
		replicaID = workerpulse.ReplicaID()
	}
	return &ExpiryWorker{
		svc:         cfg.Service,
		interval:    cfg.Interval,
		batchSize:   cfg.BatchSize,
		leaderLease: cfg.LeaderLease,
		pulse:       cfg.Pulse,
		replicaID:   replicaID,
	}
}

// Start 启动 ticker goroutine。重复 Start no-op; Stop 后可再 Start。
func (w *ExpiryWorker) Start(ctx context.Context) {
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

func (w *ExpiryWorker) loop(ctx context.Context) {
	defer close(w.done)
	t := time.NewTicker(w.interval)
	defer t.Stop()
	w.tick(ctx) // 启动即跑一次, 不等首个 ticker
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

// tick 单次到期扫描: 批量 drain 直到无到点订阅或出错 (出错下个 tick 重试)。
func (w *ExpiryWorker) tick(ctx context.Context) {
	if !w.running.CompareAndSwap(false, true) {
		return
	}
	defer w.running.Store(false)
	executed, err := runGuardedTick(ctx, w.leaderLease, w.pulse, w.replicaID, workerpulse.JobSubscriptionExpiry, func() error {
		for {
			n, workErr := w.svc.ProcessDueExpiries(ctx, w.batchSize)
			if n > 0 {
				w.expiredTotal.Add(uint64(n))
			}
			if workErr != nil {
				return workErr
			}
			if n < w.batchSize {
				return nil
			}
		}
	})
	if !executed {
		if err != nil {
			w.failedTicks.Add(1)
		}
		return
	}
	w.tickCount.Add(1)
	if err != nil {
		w.failedTicks.Add(1)
	}
}

// Stop 优雅停止并等 loop 退出 (最长 interval)。多次 Stop no-op。
func (w *ExpiryWorker) Stop() {
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

// TickOnce 测试用: 同步触发一次扫描 (生产用 Start + ticker)。
func (w *ExpiryWorker) TickOnce(ctx context.Context) {
	w.tick(ctx)
}

// TickCount 累计 tick 次数。
func (w *ExpiryWorker) TickCount() uint64 { return w.tickCount.Load() }

// ExpiredTotal 累计到期处理条数。
func (w *ExpiryWorker) ExpiredTotal() uint64 { return w.expiredTotal.Load() }

// FailedTicks 出错 tick 数。
func (w *ExpiryWorker) FailedTicks() uint64 { return w.failedTicks.Load() }
