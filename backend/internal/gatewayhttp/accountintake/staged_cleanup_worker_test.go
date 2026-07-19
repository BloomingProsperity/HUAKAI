package accountintake

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type stagedCleanupStoreStub struct {
	calls atomic.Int64
	err   error
}

func (s *stagedCleanupStoreStub) Cleanup(context.Context) error {
	s.calls.Add(1)
	return s.err
}

type stagedCleanupLeaseStub struct {
	acquired bool
	err      error
	released atomic.Bool
}

func (s *stagedCleanupLeaseStub) TryAcquire(context.Context) (bool, func(), error) {
	if s.err != nil || !s.acquired {
		return s.acquired, nil, s.err
	}
	return true, func() { s.released.Store(true) }, nil
}

func TestStagedCleanupWorkerRunOnceRequiresLease(t *testing.T) {
	store := &stagedCleanupStoreStub{}
	lease := &stagedCleanupLeaseStub{acquired: false}
	worker := NewStagedCleanupWorker(StagedCleanupWorkerConfig{Store: store, Lease: lease})
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("未获租约的轮次不应报错: %v", err)
	}
	if got := store.calls.Load(); got != 0 {
		t.Fatalf("未获租约仍执行清理: calls=%d", got)
	}

	lease.acquired = true
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("持有租约执行清理: %v", err)
	}
	if got := store.calls.Load(); got != 1 {
		t.Fatalf("清理次数=%d want=1", got)
	}
	if !lease.released.Load() {
		t.Fatal("清理结束后未释放租约")
	}
}

func TestStagedCleanupWorkerRunOncePropagatesFailures(t *testing.T) {
	storeErr := errors.New("数据库写失败")
	store := &stagedCleanupStoreStub{err: storeErr}
	lease := &stagedCleanupLeaseStub{acquired: true}
	worker := NewStagedCleanupWorker(StagedCleanupWorkerConfig{Store: store, Lease: lease})
	if err := worker.RunOnce(context.Background()); !errors.Is(err, storeErr) {
		t.Fatalf("清理错误=%v want=%v", err, storeErr)
	}
	if !lease.released.Load() {
		t.Fatal("清理失败后未释放租约")
	}

	leaseErr := errors.New("租约数据库不可用")
	worker = NewStagedCleanupWorker(StagedCleanupWorkerConfig{
		Store: store, Lease: &stagedCleanupLeaseStub{err: leaseErr},
	})
	if err := worker.RunOnce(context.Background()); !errors.Is(err, leaseErr) {
		t.Fatalf("租约错误=%v want=%v", err, leaseErr)
	}
}

func TestStagedCleanupWorkerStartsImmediatelyAndRepeats(t *testing.T) {
	store := &stagedCleanupStoreStub{}
	worker := NewStagedCleanupWorker(StagedCleanupWorkerConfig{
		Store: store, Interval: 2 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)
	deadline := time.Now().Add(time.Second)
	for store.calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := worker.Wait(context.Background()); err != nil {
		t.Fatalf("等待 worker 退出: %v", err)
	}
	if got := store.calls.Load(); got < 2 {
		t.Fatalf("worker 未启动即执行并按周期重复: calls=%d", got)
	}
}
