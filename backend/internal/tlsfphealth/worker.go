// 包 tlsfphealth — UTLS-06:TLS 指纹 profile 漂移/健康 worker。
//
// 周期校验每个 active 自定义 profile 还能不能构建出可用 uTLS ClientHello
// (复用 mimicry.ValidateProfileFields:转换 + UTLSSpec,不算 JA3 故无误杀)。
// 坏 profile(如 uTLS 升级后 cipher id 不再有效 / preset 名被改错)标记
// status='drift_detected',让运营看见并修,而非账号静默回落 builtin。
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

// Worker 周期校验 TLS profile 池,把不再可用的标 drift_detected。
type Worker struct {
	lister   Lister
	marker   DriftMarker
	interval time.Duration
	logger   *slog.Logger
	mu       sync.Mutex
	done     chan struct{}
}

func NewWorker(l Lister, m DriftMarker, interval time.Duration, logger *slog.Logger) *Worker {
	if interval <= 0 {
		interval = DefaultInterval
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{lister: l, marker: m, interval: interval, logger: logger}
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
	recs, err := w.lister.ListActive(ctx)
	if err != nil {
		w.logger.Warn("tlsfphealth: 列 active profile 失败", "err", err)
		return
	}
	for _, r := range recs {
		verr := mimicry.ValidateProfileFields(r.Fields)
		if verr == nil {
			continue
		}
		if err := w.marker.MarkDrift(ctx, r.TenantID, r.ID); err != nil {
			w.logger.Warn("tlsfphealth: 标 drift 失败", "id", r.ID, "err", err)
			continue
		}
		w.logger.Warn("tlsfphealth: TLS profile 校验失败已标 drift_detected", "id", r.ID, "name", r.Name, "reason", verr.Error())
	}
}
