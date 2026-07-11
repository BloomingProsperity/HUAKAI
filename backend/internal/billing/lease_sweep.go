package billing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

const (
	leaseSweepTickerInterval = 30 * time.Second
	leaseSweepBatchSize      = int32(100)
)

const orphanSlotReleaseReason = "slot_orphan_swept"

// leaseSweeperComponent 是本 worker 结构化日志的 component 标识(惯例同 modelsync scheduler)。
const leaseSweeperComponent = "billing_lease_sweeper"

type LeaseSweeper struct {
	pool    *pgxpool.Pool
	settler Settler
	batch   int32
	// interval/logger 可注入(测试用短 tick + 收集 handler);生产走构造器默认值。
	interval time.Duration
	logger   *slog.Logger

	mu      sync.Mutex
	running bool
	stop    chan struct{}
	done    chan struct{}
}

func NewLeaseSweeper(pool *pgxpool.Pool, settler Settler, batch int32) *LeaseSweeper {
	if batch <= 0 {
		batch = leaseSweepBatchSize
	}
	return &LeaseSweeper{
		pool:     pool,
		settler:  settler,
		batch:    batch,
		interval: leaseSweepTickerInterval,
		logger:   slog.Default(),
	}
}

func (s *LeaseSweeper) Start(ctx context.Context) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pool == nil || s.settler == nil || s.running {
		return
	}
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	s.running = true
	s.logger.InfoContext(ctx, "lease sweeper started", "component", leaseSweeperComponent)
	go s.loop(ctx)
}

func (s *LeaseSweeper) loop(ctx context.Context) {
	defer close(s.done)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-ticker.C:
			swept, err := s.sweepOnce(ctx)
			s.logRound(ctx, swept, err)
		}
	}
}

// logRound 汇总一轮孤儿回收结果:此前处理量/错误被静默丢弃,动钱补偿卡死或持续失败
// 运营完全看不见。空转轮只打 Debug,30s 周期下绝不用 Info 刷屏。
func (s *LeaseSweeper) logRound(ctx context.Context, swept int, err error) {
	// 三分支互斥:部分成功轮(swept>0 且 err≠nil)只打 Warn(已带 processed),
	// 否则同轮双发 Warn+Info 会让按 processed 求和的日志派生指标双计回收量。
	switch {
	case err != nil:
		s.logger.WarnContext(ctx, "lease sweep round failed",
			"component", leaseSweeperComponent, "processed", swept, "error", err.Error())
	case swept > 0:
		s.logger.InfoContext(ctx, "lease sweep round reclaimed orphans",
			"component", leaseSweeperComponent, "processed", swept)
	default:
		s.logger.DebugContext(ctx, "lease sweep round idle", "component", leaseSweeperComponent)
	}
}

func (s *LeaseSweeper) SweepOnce(ctx context.Context) (int, error) {
	if s == nil {
		return 0, nil
	}
	return s.sweepOnce(ctx)
}

func (s *LeaseSweeper) sweepOnce(ctx context.Context) (int, error) {
	if s == nil || s.pool == nil || s.settler == nil {
		return 0, nil
	}
	q := dbbilling.New(s.pool)
	claims, err := q.SelectExpiredReservingClaims(ctx, s.batch)
	if err != nil {
		return 0, fmt.Errorf("query expired reserving claims: %w", err)
	}

	// 逐 claim 容错:单个 claim 失败不中断整批(多副本/并发下其余孤儿仍需回收,
	// 否则 早返回会让本批剩余过期 claim 拖到下个周期、非确定性部分清扫)。
	swept := 0
	var errs []error
	for _, claim := range claims {
		err := s.settler.Abort(ctx, claim.TenantID, claim.ID, "lease_expired", fmt.Sprintf("audit-lease-%d", claim.ID), 0, nil)
		switch {
		case err == nil:
			swept++
		case errors.Is(err, ErrClaimNotReserving), errors.Is(err, ErrPostDeliverySettlementPending):
			// 并发良性:claim 已推进出 reserving，或候选查询后出现未决交付后结算恢复行；
			// 两种情况都不得继续零成本中止。
		default:
			errs = append(errs, fmt.Errorf("abort claim %d: %w", claim.ID, err))
		}
	}
	reason := orphanSlotReleaseReason
	slotSwept, err := q.SweepOrphanedSlotAcquisitions(ctx, dbbilling.SweepOrphanedSlotAcquisitionsParams{
		BatchSize:     s.batch,
		ReleaseReason: &reason,
	})
	if err != nil {
		errs = append(errs, fmt.Errorf("sweep orphan slots: %w", err))
	}
	swept += int(slotSwept)
	return swept, errors.Join(errs...)
}

func (s *LeaseSweeper) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	close(s.stop)
	done := s.done
	s.running = false
	s.mu.Unlock()
	if done != nil {
		<-done
	}
	// Stop 无 ctx,用非 context 变体;等 <-done 后再打,保证"stopped"意味着协程真退了。
	s.logger.Info("lease sweeper stopped", "component", leaseSweeperComponent)
}
