package quota

import (
	"context"
	"errors"
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
	if !store.failure.Terminal || store.job.Status != "failed" || store.job.AttemptCount != 1 {
		t.Fatalf("failure/job=%+v/%+v，want 终停 failed 且 attempt_count=1", store.failure, store.job)
	}
	if !strings.Contains(store.failure.LastError, "reserving") {
		t.Fatalf("last_error=%q，want 记录 claim 现状", store.failure.LastError)
	}
	if want := now.Add(terminalReconciliationDelay); !store.failure.NextRunAt.Equal(want) {
		t.Fatalf("next_run_at=%s，want 终态停靠 %s", store.failure.NextRunAt, want)
	}
}

// TestReconcilerReleaseAfterAbort_RevivedReconciliationNeededBacksOff 固定复活 attempt
// 运行期间只推迟旧解毒 job；若误放行或映射成 invalidated，错误和退避时间断言都会变红。
func TestReconcilerReleaseAfterAbort_RevivedReconciliationNeededBacksOff(t *testing.T) {
	now := time.Date(2026, 7, 15, 13, 30, 0, 0, time.UTC)
	store := newInvalidatedReleaseReplayStore(now)
	store.reservation.Status = ReservationReconciliationNeeded
	reconciler := NewReconciler(NewService(store), store, ReconcilerOptions{
		BaseBackoff: time.Minute,
		MaxBackoff:  time.Hour,
	})

	processed, err := reconciler.ReconcileDueJobs(context.Background(), 1, now, 10)

	if !errors.Is(err, ErrReleaseDeferredForRevival) || !IsRetryable(err) {
		t.Fatalf("err=%v，want Deferred 且可重试", err)
	}
	if strings.Contains(err.Error(), "recovery invalidated") {
		t.Fatalf("err=%v，不应映射成终停失效", err)
	}
	if processed != 0 || store.releaseCalls != 0 || store.completeCalls != 0 {
		t.Fatalf("processed/release/complete=%d/%d/%d，want 0/0/0", processed, store.releaseCalls, store.completeCalls)
	}
	if store.failure == nil {
		t.Fatal("Deferred job 未进入退避")
	}
	if store.failure.Terminal || store.job.Status != "queued" || store.job.AttemptCount != 1 {
		t.Fatalf("failure/job=%+v/%+v，want 非终停 queued 且 attempt_count=1", store.failure, store.job)
	}
	if want := now.Add(time.Minute); !store.failure.NextRunAt.Equal(want) {
		t.Fatalf("next_run_at=%s，want 普通退避 %s", store.failure.NextRunAt, want)
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
	s.job.Status = "running"
	s.job.AttemptCount++
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
	return ClaimTerminalState{Status: claimStatusReserving}, nil
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
	s.job.Status = "succeeded"
	return nil
}

func (s *invalidatedReleaseReplayStore) FailReconciliationJob(_ context.Context, input ReconciliationFailure) error {
	copy := input
	s.failure = &copy
	if input.Terminal {
		s.job.Status = "failed"
	} else {
		s.job.Status = "queued"
	}
	s.job.NextRunAt = input.NextRunAt
	lastError := input.LastError
	s.job.LastError = &lastError
	return nil
}
