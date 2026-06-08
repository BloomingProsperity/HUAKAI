// 包 proxyhealth — PROXY-04:周期探测代理池存活,带迟滞维护 status (active<->dead)。
// 解决 fail-closed 代理无自动恢复 = 自伤:代理 flap 后绑定它的账号永远黑掉,
// 直到人工介入。worker 探活恢复后账号自动回来。
package proxyhealth

import (
	"context"
	"log/slog"
	"time"
)

const (
	DefaultInterval  = time.Minute
	deadThreshold    = 3 // active->dead 需连续失败次数
	recoverThreshold = 2 // dead->active 需连续成功次数(迟滞防 flapping)
	maxPerTick       = 200
)

// ProxyTarget 是一行待探测的代理。
type ProxyTarget struct {
	ID       int64
	TenantID int64
	Status   string
	Host     string
	Port     int
}

// Lister 列出待探测的代理(active/dead,非软删;admin-disabled 不自动恢复)。
type Lister interface {
	List(ctx context.Context) ([]ProxyTarget, error)
}

// Prober 探测单个代理是否存活。
type Prober interface {
	Probe(ctx context.Context, t ProxyTarget) bool
}

// StatusStore 写代理探测结果。
type StatusStore interface {
	Touch(ctx context.Context, id int64) error                              // 仅更新 last_check_at
	SetStatus(ctx context.Context, tenantID, id int64, status string) error // 翻 status (+last_check_at)
}

type counters struct{ fails, successes int }

// Worker 周期探测代理池,带迟滞维护 status,使 flap 的代理不会被反复 dead<->active。
type Worker struct {
	lister   Lister
	prober   Prober
	store    StatusStore
	interval time.Duration
	logger   *slog.Logger
	state    map[int64]*counters
}

func NewWorker(l Lister, p Prober, s StatusStore, interval time.Duration, logger *slog.Logger) *Worker {
	if interval <= 0 {
		interval = DefaultInterval
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{lister: l, prober: p, store: s, interval: interval, logger: logger, state: map[int64]*counters{}}
}

// Start 在后台跑探测循环,直到 ctx 取消。
func (w *Worker) Start(ctx context.Context) {
	go func() {
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
	rows, err := w.lister.List(ctx)
	if err != nil {
		w.logger.Warn("proxyhealth: 列代理失败", "err", err)
		return
	}
	for _, row := range rows {
		ok := w.prober.Probe(ctx, row)
		c := w.state[row.ID]
		if c == nil {
			c = &counters{}
			w.state[row.ID] = c
		}
		newStatus := decideStatus(row.Status, ok, c)
		if newStatus == "" {
			if err := w.store.Touch(ctx, row.ID); err != nil {
				w.logger.Warn("proxyhealth: touch 失败", "id", row.ID, "err", err)
			}
			continue
		}
		if err := w.store.SetStatus(ctx, row.TenantID, row.ID, newStatus); err != nil {
			w.logger.Warn("proxyhealth: 写 status 失败", "id", row.ID, "status", newStatus, "err", err)
			continue
		}
		w.logger.Info("proxyhealth: 代理状态迁移", "id", row.ID, "from", row.Status, "to", newStatus)
	}
}

// decideStatus 是纯迟滞逻辑:返回新 status,或 "" 表示不变。同时更新 counters。
// active 连续 deadThreshold 次失败 -> dead;dead 连续 recoverThreshold 次成功 ->
// active;任一相反结果重置对侧计数,故单次 flap 不触发迁移。
func decideStatus(current string, ok bool, c *counters) string {
	if ok {
		c.successes++
		c.fails = 0
		if current != "active" && c.successes >= recoverThreshold {
			c.successes = 0
			return "active"
		}
		return ""
	}
	c.fails++
	c.successes = 0
	if current == "active" && c.fails >= deadThreshold {
		c.fails = 0
		return "dead"
	}
	return ""
}
