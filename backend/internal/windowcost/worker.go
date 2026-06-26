package windowcost

import (
	"context"
	"log/slog"
	"time"
)

const (
	// DefaultInterval 是 worker 重新聚合窗口花费的频率。
	DefaultInterval = 90 * time.Second
)

// AccountRecord 是 Lister 返回的一行活动账号记录。
type AccountRecord struct {
	ID                   int64
	TenantID             int64
	SessionWindow5hStart time.Time // 零值 → 无活动窗口 → 跳过
}

// Lister 返回拥有正值 window_cost_limit_cents 且有活动会话窗口起点的账号。
type Lister interface {
	ListLimitedAccounts(ctx context.Context) ([]AccountRecord, error)
}

// Aggregator 汇总某账号自给定时间戳起的 actual_cost。
type Aggregator interface {
	// SumWindowCost 返回该账号自 windowStart 起的 actual_cost 总额(以微美元计,
	// 与 usage_records.actual_cost 列 × 1e8 同单位 → 换算后我们以分存储)。
	// 返回值以分(1/100 USD)计 —— 调用方必须从原始 numeric 列做换算。
	SumWindowCost(ctx context.Context, accountID int64, windowStart time.Time) (cents int64, err error)
}

// Worker 周期性地把窗口花费聚合进 Cache。
type Worker struct {
	lister     Lister
	aggregator Aggregator
	cache      *Cache
	interval   time.Duration
	logger     *slog.Logger
}

// NewWorker 构造一个 Worker。interval<=0 时使用 DefaultInterval。
func NewWorker(lister Lister, aggregator Aggregator, cache *Cache, interval time.Duration, logger *slog.Logger) *Worker {
	if interval <= 0 {
		interval = DefaultInterval
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{
		lister:     lister,
		aggregator: aggregator,
		cache:      cache,
		interval:   interval,
		logger:     logger,
	}
}

// Start 启动后台聚合循环。它立即返回;循环一直运行直到 ctx 被取消。
func (w *Worker) Start(ctx context.Context) {
	go func() {
		// 启动时立即跑一次,之后按 ticker 触发。
		w.tick(ctx)
		t := time.NewTicker(w.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				w.tick(ctx)
			}
		}
	}()
}

func (w *Worker) tick(ctx context.Context) {
	accounts, err := w.lister.ListLimitedAccounts(ctx)
	if err != nil {
		w.logger.Warn("windowcost: list limited accounts failed", "err", err)
		// fail-open:缓存维持原样
		return
	}
	for _, a := range accounts {
		if a.SessionWindow5hStart.IsZero() {
			// 无活动窗口 —— 跳过;缓存条目保持陈旧 → fail-open。
			continue
		}
		cents, err := w.aggregator.SumWindowCost(ctx, a.ID, a.SessionWindow5hStart)
		if err != nil {
			w.logger.Warn("windowcost: aggregate cost failed", "account_id", a.ID, "err", err)
			// fail-open:不更新该账号的缓存
			continue
		}
		w.cache.Set(a.ID, cents)
	}
}
