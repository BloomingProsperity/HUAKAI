// HUAKAI · iKun

package subscription

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultAutoRenewInterval 自动续费扫描周期 (默认 5 分钟)。续费是 money 动作,
// 不需要像到期降级那样秒级响应; 拉长周期降低无谓空扫与扣款竞争。
const DefaultAutoRenewInterval = 5 * time.Minute

// DefaultAutoRenewBatchSize 单次扫描批量上限 (默认 200)。
const DefaultAutoRenewBatchSize = 200

// AutoRenewWorker 后台 ticker: 周期扫"到点且 auto_renew=true"的订阅, 逐条尝试
// "扣钱包余额 → 续期"。单 goroutine, 接 context cancellation 优雅退出。
// 默认在 wiring 处不启动 (HUAKAI_SUBSCRIPTION_AUTO_RENEW_ENABLED 默认 false),
// 翻 KNOB 才激活自动扣费续期 —— 关时现有 auto_renew=true 订阅行为零变化。
type AutoRenewWorker struct {
	svc       *Service
	interval  time.Duration
	batchSize int

	mu      sync.Mutex
	started bool
	stop    chan struct{}
	done    chan struct{}

	running      atomic.Bool   // 防 tick 重入 (TickOnce 与 loop 并发)
	tickCount    atomic.Uint64 // 累计 tick
	renewedTotal atomic.Uint64 // 累计成功续费条数
	skippedTotal atomic.Uint64 // 累计跳过条数 (余额不足 / 已续过 / 状态变更)
	failedTicks  atomic.Uint64 // 出错 tick 数 (运维 metrics)
}

// AutoRenewWorkerConfig 构造参数。
type AutoRenewWorkerConfig struct {
	Service   *Service
	Interval  time.Duration // 0 用 DefaultAutoRenewInterval
	BatchSize int           // <=0 用 DefaultAutoRenewBatchSize
}

// NewAutoRenewWorker 构造自动续费 worker。
func NewAutoRenewWorker(cfg AutoRenewWorkerConfig) *AutoRenewWorker {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultAutoRenewInterval
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultAutoRenewBatchSize
	}
	return &AutoRenewWorker{
		svc:       cfg.Service,
		interval:  cfg.Interval,
		batchSize: cfg.BatchSize,
	}
}

// Start 启动 ticker goroutine。重复 Start no-op; Stop 后可再 Start。
func (w *AutoRenewWorker) Start(ctx context.Context) {
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

func (w *AutoRenewWorker) loop(ctx context.Context) {
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

// tick 单次续费扫描: 批量 drain 直到无到点订阅或出错 (出错下个 tick 重试)。
func (w *AutoRenewWorker) tick(ctx context.Context) {
	if !w.running.CompareAndSwap(false, true) {
		return
	}
	defer w.running.Store(false)
	w.tickCount.Add(1)
	for {
		res, err := w.svc.ProcessAutoRenewal(ctx, w.batchSize)
		if res.Renewed > 0 {
			w.renewedTotal.Add(uint64(res.Renewed))
		}
		if res.Skipped > 0 {
			w.skippedTotal.Add(uint64(res.Skipped))
		}
		if err != nil {
			w.failedTicks.Add(1)
			return // 下个 tick 重试剩余
		}
		if res.Scanned < w.batchSize {
			return // 已 drain 完
		}
	}
}

// Stop 优雅停止并等 loop 退出 (最长 interval)。多次 Stop no-op。
func (w *AutoRenewWorker) Stop() {
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
func (w *AutoRenewWorker) TickOnce(ctx context.Context) {
	w.tick(ctx)
}

// TickCount 累计 tick 次数。
func (w *AutoRenewWorker) TickCount() uint64 { return w.tickCount.Load() }

// RenewedTotal 累计成功续费条数。
func (w *AutoRenewWorker) RenewedTotal() uint64 { return w.renewedTotal.Load() }

// SkippedTotal 累计跳过条数 (余额不足 / 已续过 / 状态变更)。
func (w *AutoRenewWorker) SkippedTotal() uint64 { return w.skippedTotal.Load() }

// FailedTicks 出错 tick 数。
func (w *AutoRenewWorker) FailedTicks() uint64 { return w.failedTicks.Load() }

// AutoRenewBatchResult 一批自动续费的处理结果计数。
type AutoRenewBatchResult struct {
	Scanned int // 本批扫到的候选订阅数
	Renewed int // 成功扣款 + 续期的条数
	Skipped int // 跳过的条数 (余额不足 / 已续过 / 重查不再 due)
}

// ProcessAutoRenewal 扫一批"到点且 auto_renew=true"的订阅 (system 触发), 逐条尝试
// "扣钱包余额 → 续期"。单条失败不阻断其余 (记 lastErr 返回), 由 worker 下个 tick 重试。
//
// 每条续费的原子性/幂等性/余额不足跳过逻辑全在 store.TryAutoRenewSubscription 的单事务里
// (扣钱与续期不可分裂; 幂等锚防双扣; 余额不足绝不扣只跳过)。本方法只做批编排与计数。
func (s *Service) ProcessAutoRenewal(ctx context.Context, limit int) (AutoRenewBatchResult, error) {
	now := s.now()
	due, err := s.store.ListAutoRenewDue(ctx, now, limit)
	if err != nil {
		return AutoRenewBatchResult{}, err
	}
	out := AutoRenewBatchResult{Scanned: len(due)}
	var lastErr error
	for _, sub := range due {
		res, err := s.store.TryAutoRenewSubscription(ctx, autoRenewRecord{
			TenantID:       sub.TenantID,
			SubscriptionID: sub.ID,
			Now:            s.now(),
		})
		if err != nil {
			lastErr = err
			continue
		}
		if res.Renewed {
			out.Renewed++
		} else {
			out.Skipped++
		}
	}
	return out, lastErr
}
