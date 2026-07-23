package autolisting

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/workerlease"
)

// LeaderLease 保证多副本部署每轮只有一个执行者。用 session 变体(非一次性 TryAcquire):
// 租约连接中途断开时 advisory lock 会被 PostgreSQL 释放、另一副本可接管,故本轮在两个耗时
// 步骤(保鲜、上架)之间显式 Healthy 检查,失效即弃本轮,收窄双执行者窗口。
type LeaderLease = workerlease.SessionProvider

// Refresher 是保鲜步骤(RefreshReversedAccounts),便于测试替身。
type Refresher interface {
	RefreshReversedAccounts(context.Context) (RefreshResult, error)
}

// Promoter 是自动上架步骤(ProcessPending),便于测试替身。
type Promoter interface {
	ProcessPending(context.Context) (Result, error)
}

// SettingsGate 每 tick 重新读总闸,使运营开关无需重启即时生效。
type SettingsGate interface {
	Enabled(context.Context) bool
}

type WorkerConfig struct {
	Interval    time.Duration
	RunOnStart  bool
	LeaderLease LeaderLease
}

// Worker 周期性驱动自动上架管道:先保鲜反转号(投箱),再处理待上架发现。总闸关时每 tick
// 直接跳过,不触任何上游调用或写库。
type Worker struct {
	refresher Refresher
	promoter  Promoter
	gate      SettingsGate
	cfg       WorkerConfig

	stopOnce sync.Once
	cancel   context.CancelFunc
	done     chan struct{}
}

func NewWorker(refresher Refresher, promoter Promoter, gate SettingsGate, cfg WorkerConfig) *Worker {
	if cfg.Interval <= 0 {
		cfg.Interval = 3 * time.Hour
	}
	return &Worker{refresher: refresher, promoter: promoter, gate: gate, cfg: cfg}
}

func (w *Worker) Start(ctx context.Context) func() {
	if w == nil || w.promoter == nil {
		return func() {}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	w.done = make(chan struct{})
	go w.run(runCtx)
	return w.Stop
}

func (w *Worker) Stop() {
	if w == nil {
		return
	}
	w.stopOnce.Do(func() {
		if w.cancel != nil {
			w.cancel()
		}
		if w.done != nil {
			<-w.done
		}
	})
}

func (w *Worker) run(ctx context.Context) {
	defer close(w.done)
	if w.cfg.RunOnStart {
		w.tick(ctx, "startup")
	}
	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.tick(ctx, "periodic")
		}
	}
}

func (w *Worker) tick(ctx context.Context, reason string) {
	// 总闸每 tick 现读:关 → 纯人工挡,不抢 leader、不触上游。
	if w.gate == nil || !w.gate.Enabled(ctx) {
		return
	}
	var session workerlease.Session
	if w.cfg.LeaderLease != nil {
		acquired, s, err := w.cfg.LeaderLease.TryAcquireSession(ctx)
		if err != nil {
			slog.WarnContext(ctx, "auto-listing leader lease failed",
				"component", "auto_listing_worker", "event_class", "leader_lease_failed",
				"reason", reason, "error", err.Error())
			return
		}
		if !acquired || s == nil {
			return
		}
		session = s
		defer session.Release()
	}

	if w.refresher != nil {
		if !w.leaderHealthy(ctx, session, reason) {
			return
		}
		refreshResult, err := w.refresher.RefreshReversedAccounts(ctx)
		if err != nil {
			slog.WarnContext(ctx, "auto-listing refresh round failed",
				"component", "auto_listing_worker", "event_class", "refresh_round_failed",
				"reason", reason, "error", err.Error())
		} else {
			slog.InfoContext(ctx, "auto-listing refresh round done",
				"component", "auto_listing_worker", "event_class", "refresh_round_done",
				"reason", reason, "accounts", refreshResult.Accounts, "ok", refreshResult.OK,
				"failed", refreshResult.Failed, "invested", refreshResult.Invested)
		}
	}

	// 保鲜可能耗时较久,上架前复检租约会话:失效则弃本轮,避免与接管副本重复 promote。
	if !w.leaderHealthy(ctx, session, reason) {
		return
	}
	promoteResult, err := w.promoter.ProcessPending(ctx)
	if err != nil {
		slog.WarnContext(ctx, "auto-listing promote round failed",
			"component", "auto_listing_worker", "event_class", "promote_round_failed",
			"reason", reason, "error", err.Error())
		return
	}
	slog.InfoContext(ctx, "auto-listing promote round done",
		"component", "auto_listing_worker", "event_class", "promote_round_done",
		"reason", reason, "scanned", promoteResult.Scanned, "promoted", promoteResult.Promoted,
		"skipped_manual_vendor", promoteResult.SkippedManualVendor,
		"skipped_no_price", promoteResult.SkippedNoPrice, "failed", promoteResult.Failed)
}

// leaderHealthy 在耗时步骤前复检租约会话仍持有(session 为 nil = 未配置 lease,单副本部署)。
func (w *Worker) leaderHealthy(ctx context.Context, session workerlease.Session, reason string) bool {
	if session == nil {
		return true
	}
	if err := session.Healthy(ctx); err != nil {
		slog.WarnContext(ctx, "auto-listing leader session lost",
			"component", "auto_listing_worker", "event_class", "leader_session_lost",
			"reason", reason, "error", err.Error())
		return false
	}
	return true
}
