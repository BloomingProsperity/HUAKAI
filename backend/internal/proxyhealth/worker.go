// 包 proxyhealth — PROXY-04:周期探测代理池存活,带迟滞维护 status (active<->dead)。
// 解决 fail-closed 代理无自动恢复 = 自伤:代理 flap 后绑定它的账号永远黑掉,
// 直到人工介入。worker 探活恢复后账号自动回来。
package proxyhealth

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/workerlease"
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
	Touch(ctx context.Context, tenantID, id int64) error                    // 仅更新 last_check_at
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
	lease    workerlease.SessionProvider
	state    map[int64]*counters
	mu       sync.Mutex
	done     chan struct{}
}

type WorkerOption func(*Worker)

// WithLeaderLease 让多副本只由一个实例持有探测职责。租约会话失效时，当前
// leader 立即停止探测并重新参与选主，避免数据库重连后多个旧 leader 并存。
func WithLeaderLease(lease workerlease.SessionProvider) WorkerOption {
	return func(w *Worker) { w.lease = lease }
}

func NewWorker(l Lister, p Prober, s StatusStore, interval time.Duration, logger *slog.Logger, opts ...WorkerOption) *Worker {
	if interval <= 0 {
		interval = DefaultInterval
	}
	if logger == nil {
		logger = slog.Default()
	}
	w := &Worker{lister: l, prober: p, store: s, interval: interval, logger: logger, state: map[int64]*counters{}}
	for _, opt := range opts {
		if opt != nil {
			opt(w)
		}
	}
	return w
}

// Start 在后台跑探测循环,直到 ctx 取消。
func (w *Worker) Start(ctx context.Context) {
	if w == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w.mu.Lock()
	if w.done != nil {
		w.mu.Unlock()
		return
	}
	done := make(chan struct{})
	w.done = done
	w.mu.Unlock()
	go func() {
		defer close(done)
		w.run(ctx)
	}()
}

func (w *Worker) run(ctx context.Context) {
	if w.lease == nil {
		w.runWithoutLease(ctx)
		return
	}
	for ctx.Err() == nil {
		acquired, session, err := w.lease.TryAcquireSession(ctx)
		if err != nil {
			w.logger.Warn("proxyhealth: 取得 leader 租约失败", "err", err)
			if !waitInterval(ctx, w.interval) {
				return
			}
			continue
		}
		if !acquired || session == nil {
			if !waitInterval(ctx, w.interval) {
				return
			}
			continue
		}
		w.runAsLeader(ctx, session)
		session.Release()
		if ctx.Err() == nil && !waitInterval(ctx, w.interval) {
			return
		}
	}
}

func (w *Worker) runWithoutLease(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

func (w *Worker) runAsLeader(ctx context.Context, session workerlease.Session) {
	if err := session.Healthy(ctx); err != nil {
		w.logger.Warn("proxyhealth: leader 租约会话失效", "err", err)
		return
	}
	// leader 取得租约后立即跑一轮，避免新集群必须等待完整探测周期才有状态。
	w.tick(ctx)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := session.Healthy(ctx); err != nil {
				w.logger.Warn("proxyhealth: leader 租约会话失效", "err", err)
				return
			}
			w.tick(ctx)
		}
	}
}

func waitInterval(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Wait 等待后台循环退出。调用方应先取消传给 Start 的 context。
func (w *Worker) Wait(ctx context.Context) error {
	if w == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w.mu.Lock()
	done := w.done
	w.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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
			if err := w.store.Touch(ctx, row.TenantID, row.ID); err != nil {
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
