// 包 tlsfphealth 提供 TLS 指纹 profile 漂移与健康检查。
//
// 周期校验每个 active 自定义 profile 是否仍能转换成 Rust IPC 动态 profile。
// 坏 profile 会标记为 drift_detected，避免账号运行时才发现出口不可执行。
package tlsfphealth

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

const (
	DefaultInterval = 30 * time.Minute
	maxPerTick      = 500
)

// ProfileRecord 是一行待校验的 active TLS profile。
type ProfileRecord struct {
	ID       int64
	TenantID int64
	Name     string
	Fields   mimicry.ProfileFields
}

// Lister 列出 active 自定义 profile。
type Lister interface {
	ListActive(ctx context.Context) ([]ProfileRecord, error)
}

// DriftMarker 把校验失败的 profile 标 drift_detected。
type DriftMarker interface {
	MarkDrift(ctx context.Context, tenantID, id int64) error
}

// LeaderLease 保证多副本部署中每轮只有一个实例写漂移状态。
type LeaderLease interface {
	TryAcquire(context.Context) (bool, func(), error)
}

type Option func(*Worker)

func WithLeaderLease(lease LeaderLease) Option {
	return func(w *Worker) { w.leaderLease = lease }
}

// Worker 周期校验 TLS profile 池,把不再可用的标 drift_detected。
type Worker struct {
	lister      Lister
	marker      DriftMarker
	interval    time.Duration
	logger      *slog.Logger
	leaderLease LeaderLease
	mu          sync.Mutex
	done        chan struct{}
}

func NewWorker(l Lister, m DriftMarker, interval time.Duration, logger *slog.Logger, opts ...Option) *Worker {
	if interval <= 0 {
		interval = DefaultInterval
	}
	if logger == nil {
		logger = slog.Default()
	}
	w := &Worker{lister: l, marker: m, interval: interval, logger: logger}
	for _, opt := range opts {
		if opt != nil {
			opt(w)
		}
	}
	return w
}

// Start 在后台跑校验循环,直到 ctx 取消。
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
	if w == nil || w.lister == nil || w.marker == nil {
		return
	}
	if w.leaderLease != nil {
		acquired, release, err := w.leaderLease.TryAcquire(ctx)
		if err != nil {
			w.logger.WarnContext(ctx, "tlsfphealth: 获取多副本租约失败", "error_class", "coordination_dependency_unavailable")
			return
		}
		if !acquired {
			return
		}
		if release == nil {
			w.logger.WarnContext(ctx, "tlsfphealth: 多副本租约返回无效释放函数", "error_class", "coordination_contract_invalid")
			return
		}
		defer release()
	}
	recs, err := w.lister.ListActive(ctx)
	if err != nil {
		w.logger.WarnContext(ctx, "tlsfphealth: 列 active profile 失败", "error_class", "database_read_failed")
		return
	}
	for _, r := range recs {
		verr := mimicry.ValidateProfileFields(r.Fields)
		if verr == nil {
			continue
		}
		if err := w.marker.MarkDrift(ctx, r.TenantID, r.ID); err != nil {
			w.logger.WarnContext(ctx, "tlsfphealth: 标 drift 失败", "id", r.ID, "error_class", "database_write_failed")
			continue
		}
		w.logger.WarnContext(ctx, "tlsfphealth: TLS profile 校验失败已标 drift_detected", "id", r.ID, "name", r.Name, "error_class", "profile_contract_invalid")
	}
}
