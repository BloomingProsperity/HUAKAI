package billing

import (
	"context"
	"errors"
	"fmt"
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

type LeaseSweeper struct {
	pool    *pgxpool.Pool
	settler Settler
	batch   int32

	mu      sync.Mutex
	running bool
	stop    chan struct{}
	done    chan struct{}
}

func NewLeaseSweeper(pool *pgxpool.Pool, settler Settler, batch int32) *LeaseSweeper {
	if batch <= 0 {
		batch = leaseSweepBatchSize
	}
	return &LeaseSweeper{pool: pool, settler: settler, batch: batch}
}

func (s *LeaseSweeper) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s == nil || s.pool == nil || s.settler == nil || s.running {
		return
	}
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	s.running = true
	go s.loop(ctx)
}

func (s *LeaseSweeper) loop(ctx context.Context) {
	defer close(s.done)
	ticker := time.NewTicker(leaseSweepTickerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-ticker.C:
			_, _ = s.sweepOnce(ctx)
		}
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
	// 否则 #8-P2 早返回会让本批剩余过期 claim 拖到下个周期、非确定性部分清扫)。
	swept := 0
	var errs []error
	for _, claim := range claims {
		err := s.settler.Abort(ctx, claim.TenantID, claim.ID, "lease_expired", fmt.Sprintf("audit-lease-%d", claim.ID), 0, nil)
		switch {
		case err == nil:
			swept++
		case errors.Is(err, ErrClaimNotReserving):
			// 并发良性:claim 已被真实请求路径或另一副本推进出 reserving 态 → 不再孤儿,跳过。
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
}
