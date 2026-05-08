// pasr_aging_worker.go — PASR-lite A5: 段表老化 + LRU evict 后台 worker。
//
// 一个 goroutine, 每 5min ticker 调 SegmentTable.EvictExpired 把超过 30min
// 无 cache_read 的段清掉 (synthesis D8 时间触发部分; LRU evict 在
// LookupOrCreate 写入路径同步触发, 不需后台).
//
// 接 context cancellation 优雅退出。
package pool

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// PASRAgingWorker 后台 ticker, 周期清理过期段。
type PASRAgingWorker struct {
	segments *SegmentTable
	interval time.Duration
	now      func() time.Time

	// 运行状态
	mu      sync.Mutex
	running bool
	stop    chan struct{}
	done    chan struct{}

	// metrics (atomic 计数, 不竞争)
	tickCount    atomic.Uint64 // 累计 tick 次数
	evictedTotal atomic.Uint64 // 累计 evict 段数
}

// PASRAgingWorkerConfig 构造期参数。
type PASRAgingWorkerConfig struct {
	Segments *SegmentTable
	Interval time.Duration // 0 用 5 * time.Minute
	Now      func() time.Time
}

// DefaultAgingInterval 默认 ticker 周期 (synthesis D8 5min)。
const DefaultAgingInterval = 5 * time.Minute

// NewPASRAgingWorker 构造实例。
func NewPASRAgingWorker(cfg PASRAgingWorkerConfig) *PASRAgingWorker {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultAgingInterval
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &PASRAgingWorker{
		segments: cfg.Segments,
		interval: cfg.Interval,
		now:      cfg.Now,
	}
}

// Start 启动 ticker goroutine。重复 Start no-op (idempotent)。
// 调 Stop 后再 Start 是允许的: 重置 stop/done 通道。
func (w *PASRAgingWorker) Start(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running {
		return
	}
	if w.segments == nil {
		return
	}
	w.stop = make(chan struct{})
	w.done = make(chan struct{})
	w.running = true
	go w.loop(ctx)
}

// loop 主循环: 周期 tick + 监听 context cancellation。
func (w *PASRAgingWorker) loop(ctx context.Context) {
	defer close(w.done)
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case <-t.C:
			w.tick()
		}
	}
}

// tick 单次老化扫描。在 loop 内同步调用, 不并发执行多次。
func (w *PASRAgingWorker) tick() {
	w.tickCount.Add(1)
	n := w.segments.EvictExpired(w.now())
	if n > 0 {
		w.evictedTotal.Add(uint64(n))
		AddEvictions(int64(n))
	}
	// 段表大小快照, 5min 一次, 不每请求开销
	SetSegmentCount(int64(w.segments.Size()))
}

// Stop 优雅停止 worker, 等 loop 退出 (最长 interval)。
// 调多次 Stop 是 no-op (idempotent)。Stop 后 worker 可被再次 Start。
func (w *PASRAgingWorker) Stop() {
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	close(w.stop)
	w.running = false
	doneChan := w.done
	w.mu.Unlock()
	if doneChan != nil {
		<-doneChan
	}
}

// TickCount 累计 tick 次数 (测试 + 运维 metrics 读)。
func (w *PASRAgingWorker) TickCount() uint64 {
	return w.tickCount.Load()
}

// EvictedTotal 累计被 evict 的段数。
func (w *PASRAgingWorker) EvictedTotal() uint64 {
	return w.evictedTotal.Load()
}

// TickOnce 用于测试: 同步触发一次老化扫描, 不依赖 ticker。
// 生产代码不应调用 (用 Start + ticker)。
func (w *PASRAgingWorker) TickOnce() {
	w.tick()
}
