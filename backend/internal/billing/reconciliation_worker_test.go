package billing

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPendingReconciliationWorkerFinalizesNoUsageRowsWithGraceCutoff(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	store := &recordingPendingNoUsageFinalizer{finalized: 3}
	worker := NewPendingReconciliationWorker(store, time.Minute, 5*time.Minute, 25)

	got, err := worker.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got != 3 {
		t.Fatalf("finalized=%d want 3", got)
	}
	if store.calls != 1 {
		t.Fatalf("store calls=%d want 1", store.calls)
	}
	if !store.cutoff.Equal(now.Add(-5 * time.Minute)) {
		t.Fatalf("cutoff=%s want %s", store.cutoff, now.Add(-5*time.Minute))
	}
	if store.limit != 25 {
		t.Fatalf("limit=%d want 25", store.limit)
	}
	if store.source != PendingReconciliationSourceStreamNoUsageFinalized {
		t.Fatalf("source=%q want %q", store.source, PendingReconciliationSourceStreamNoUsageFinalized)
	}
	if !store.reconciledAt.Equal(now) {
		t.Fatalf("reconciledAt=%s want %s", store.reconciledAt, now)
	}
}

func TestPendingReconciliationWorkerPropagatesFinalizeErrors(t *testing.T) {
	wantErr := errors.New("db unavailable")
	store := &recordingPendingNoUsageFinalizer{err: wantErr}
	worker := NewPendingReconciliationWorker(store, time.Minute, 5*time.Minute, 10)

	if _, err := worker.RunOnce(context.Background(), time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)); !errors.Is(err, wantErr) {
		t.Fatalf("RunOnce err=%v want %v", err, wantErr)
	}
}

type recordingPendingNoUsageFinalizer struct {
	calls        int
	cutoff       time.Time
	limit        int32
	source       string
	reconciledAt time.Time
	finalized    int
	err          error
}

func (s *recordingPendingNoUsageFinalizer) FinalizePendingNoUsage(ctx context.Context, cutoff time.Time, limit int32, source string, reconciledAt time.Time) (int, error) {
	if ctx == nil {
		return 0, errors.New("nil context")
	}
	s.calls++
	s.cutoff = cutoff
	s.limit = limit
	s.source = source
	s.reconciledAt = reconciledAt
	return s.finalized, s.err
}
