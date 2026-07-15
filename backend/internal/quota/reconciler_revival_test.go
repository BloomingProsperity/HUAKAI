package quota

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// TestReconcilerReleaseAfterAbort_RevivedClaimTerminallyFails 固定旧恢复任务
// 只能依赖 Release 事务内的复活守卫；若守卫仍留在事务外或被删除，事务位置或终态断言会变红。
func TestReconcilerReleaseAfterAbort_RevivedClaimTerminallyFails(t *testing.T) {
	now := time.Date(2026, 7, 15, 13, 0, 0, 0, time.UTC)
	store := newInvalidatedReleaseReplayStore(now)
	reconciler := NewReconciler(NewService(store), store, ReconcilerOptions{
		BaseBackoff: time.Minute,
		MaxBackoff:  time.Hour,
	})

	processed, err := reconciler.ReconcileDueJobs(context.Background(), 1, now, 10)

	if err == nil || !strings.Contains(err.Error(), "recovery invalidated") {
		t.Fatalf("err=%v，want 恢复失效错误", err)
	}
	if processed != 0 {
		t.Fatalf("processed=%d，want 0", processed)
	}
	if store.releaseCalls != 0 || store.completeCalls != 0 {
		t.Fatalf("release/complete calls=%d/%d，want 0/0", store.releaseCalls, store.completeCalls)
	}
	if store.claimReadsInsideTx != 1 || store.claimReadsOutsideTx != 0 {
		t.Fatalf("claim 事务内/外读取=%d/%d，want 1/0", store.claimReadsInsideTx, store.claimReadsOutsideTx)
	}
	if store.called {
		t.Fatal("失效 sentinel 不应再进入恢复准备或入队")
	}
	if store.failure == nil {
		t.Fatal("失效 job 未标 failed")
	}
	if !strings.Contains(store.failure.LastError, "reserving") {
		t.Fatalf("last_error=%q，want 记录 claim 现状", store.failure.LastError)
	}
	if want := now.Add(terminalReconciliationDelay); !store.failure.NextRunAt.Equal(want) {
		t.Fatalf("next_run_at=%s，want 终态停靠 %s", store.failure.NextRunAt, want)
	}
}

type invalidatedReleaseReplayStore struct {
	noTxReserveStore
	now                 time.Time
	job                 ReconciliationJob
	reservation         Reservation
	failure             *ReconciliationFailure
	releaseCalls        int
	completeCalls       int
	inTx                bool
	claimReadsInsideTx  int
	claimReadsOutsideTx int
}

func newInvalidatedReleaseReplayStore(now time.Time) *invalidatedReleaseReplayStore {
	reservationID := int64(22)
	return &invalidatedReleaseReplayStore{
		now: now,
		job: ReconciliationJob{
			TenantID:      1,
			ID:            11,
			ClaimID:       33,
			ReservationID: &reservationID,
			Kind:          reconciliationKindReleaseAfterAbort,
			Status:        "queued",
			NextRunAt:     now.Add(-time.Minute),
		},
		reservation: Reservation{
			TenantID:       1,
			ID:             reservationID,
			ClaimID:        33,
			Status:         ReservationReserved,
			PredictedCost:  decimal.NewFromInt(4),
			ReservedUnits:  decimal.NewFromInt(1),
			PolicySnapshot: marshalPolicySnapshot(nil, nil),
		},
	}
}

func (s *invalidatedReleaseReplayStore) WithTx(ctx context.Context, run func(PGStore) error) error {
	s.inTx = true
	defer func() { s.inTx = false }()
	return run(s)
}

func (s *invalidatedReleaseReplayStore) ListDueReconciliationJobs(context.Context, int64, time.Time, int) ([]ReconciliationJob, error) {
	return []ReconciliationJob{s.job}, nil
}

func (s *invalidatedReleaseReplayStore) MarkReconciliationJobRunning(context.Context, int64, int64) error {
	return nil
}

func (s *invalidatedReleaseReplayStore) GetReservationByClaimForUpdate(context.Context, int64, int64) (Reservation, error) {
	return s.reservation, nil
}

func (s *invalidatedReleaseReplayStore) GetClaimTerminalState(context.Context, int64, int64) (ClaimTerminalState, error) {
	if s.inTx {
		s.claimReadsInsideTx++
	} else {
		s.claimReadsOutsideTx++
	}
	return ClaimTerminalState{Status: "reserving"}, nil
}

func (s *invalidatedReleaseReplayStore) ReleaseConcurrencySlots(context.Context, int64, int64, string) error {
	return nil
}

func (s *invalidatedReleaseReplayStore) ReleaseReservation(context.Context, ReservationRelease) error {
	s.releaseCalls++
	return nil
}

func (s *invalidatedReleaseReplayStore) InsertAuditEvent(context.Context, AuditEvent) (int64, error) {
	return 1, nil
}

func (s *invalidatedReleaseReplayStore) CompleteReconciliationJob(context.Context, int64, int64) error {
	s.completeCalls++
	return nil
}

func (s *invalidatedReleaseReplayStore) FailReconciliationJob(_ context.Context, input ReconciliationFailure) error {
	copy := input
	s.failure = &copy
	return nil
}
