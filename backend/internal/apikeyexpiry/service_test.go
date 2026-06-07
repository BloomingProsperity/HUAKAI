package apikeyexpiry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestServiceSweepExpiredKeysLoopsBoundedBatchesUntilEmpty(t *testing.T) {
	ctx := context.Background()
	q := &fakeExpiryQueries{batches: []int64{2, 2, 0}}
	svc := NewService(q, WithBatchLimit(2))

	changed, err := svc.SweepExpiredKeys(ctx)
	if err != nil {
		t.Fatalf("SweepExpiredKeys: %v", err)
	}
	if changed != 4 {
		t.Fatalf("changed=%d want 4", changed)
	}
	if len(q.limits) != 3 {
		t.Fatalf("calls=%d want 3", len(q.limits))
	}
	for i, got := range q.limits {
		if got != 2 {
			t.Fatalf("call %d batch_limit=%d want 2", i, got)
		}
	}
}

func TestServiceSweepExpiredKeysStopsAndReturnsPartialCountOnError(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("db down")
	q := &fakeExpiryQueries{batches: []int64{3}, err: wantErr}
	svc := NewService(q, WithBatchLimit(10))

	changed, err := svc.SweepExpiredKeys(ctx)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err=%v want %v", err, wantErr)
	}
	if changed != 3 {
		t.Fatalf("changed=%d want partial count 3", changed)
	}
	if len(q.limits) != 2 {
		t.Fatalf("calls=%d want 2", len(q.limits))
	}
}

func TestServiceNilReceiverAndWorkerNilReceiverAreSafe(t *testing.T) {
	var svc *Service
	if changed, err := svc.SweepExpiredKeys(context.Background()); err != nil || changed != 0 {
		t.Fatalf("nil service SweepExpiredKeys=(%d,%v), want (0,nil)", changed, err)
	}
	var worker *Worker
	worker.Start(context.Background())
	worker.Stop()
	if changed, err := worker.RunOnce(context.Background()); err != nil || changed != 0 {
		t.Fatalf("nil worker RunOnce=(%d,%v), want (0,nil)", changed, err)
	}
}

func TestWorkerStartStopRunsWithoutWaitingFullInterval(t *testing.T) {
	ctx := context.Background()
	q := &fakeExpiryQueries{batches: []int64{0, 0}}
	worker := NewWorker(WorkerConfig{
		Service:  NewService(q, WithBatchLimit(1)),
		Interval: time.Hour,
	})
	worker.Start(ctx)
	worker.Stop()
	worker.Stop()
}

type fakeExpiryQueries struct {
	batches []int64
	limits  []int32
	err     error
}

func (f *fakeExpiryQueries) ExpireActiveAPIKeys(ctx context.Context, batchLimit int32) (int64, error) {
	f.limits = append(f.limits, batchLimit)
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(f.batches) == 0 {
		return 0, f.err
	}
	n := f.batches[0]
	f.batches = f.batches[1:]
	return n, nil
}
