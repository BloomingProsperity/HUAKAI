package servermonitor

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeCollector struct {
	mu         sync.Mutex
	collection Collection
	err        error
	calls      int
}

func (f *fakeCollector) Collect(context.Context) (Collection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.collection, f.err
}

type fakeSnapshotStore struct {
	mu        sync.Mutex
	snapshots []Snapshot
	cutoffs   []time.Time
	writeErr  error
}

func (f *fakeSnapshotStore) WriteSnapshot(_ context.Context, snapshot Snapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	f.snapshots = append(f.snapshots, snapshot)
	return nil
}

func (f *fakeSnapshotStore) Cleanup(_ context.Context, cutoff time.Time, _ int) (CleanupResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cutoffs = append(f.cutoffs, cutoff)
	return CleanupResult{}, nil
}

type fakeLease struct {
	mu       sync.Mutex
	acquired bool
	busy     bool
	err      error
	released int
}

func (l *fakeLease) TryAcquire(context.Context) (bool, func(), error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return false, nil, l.err
	}
	if l.busy {
		return false, nil, nil
	}
	l.busy = true
	l.acquired = true
	var once sync.Once
	return true, func() {
		once.Do(func() {
			l.mu.Lock()
			l.busy = false
			l.released++
			l.mu.Unlock()
		})
	}, nil
}

type manualTicker struct {
	ch chan time.Time
}

func (t *manualTicker) Chan() <-chan time.Time { return t.ch }
func (t *manualTicker) Stop()                  {}

func TestWorkerStartsWithImmediateSnapshotAndReleasesLease(t *testing.T) {
	now := time.Date(2026, 7, 19, 4, 0, 0, 0, time.UTC)
	collector := &fakeCollector{collection: validCollection(now)}
	store := &fakeSnapshotStore{}
	nodeLease := &fakeLease{}
	cleanupLease := &fakeLease{}
	tickers := make(chan *manualTicker, 2)
	worker, err := NewWorker(WorkerConfig{
		Identity:        Identity{NodeID: "node-test-01", DisplayName: "测试节点", Source: IdentitySourceConfigured, Stable: true},
		Session:         Session{ID: validTestSnapshot(now).SessionID, StartedAt: now.Add(-time.Second)},
		Collector:       collector,
		Store:           store,
		NodeLease:       nodeLease,
		CleanupLease:    cleanupLease,
		Interval:        time.Minute,
		CleanupInterval: time.Hour,
		Retention:       DefaultRetention,
		Now:             func() time.Time { return now },
		NewTicker: func(time.Duration) ticker {
			t := &manualTicker{ch: make(chan time.Time, 1)}
			tickers <- t
			return t
		},
	})
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	preStopCtx, preStopCancel := context.WithTimeout(context.Background(), time.Second)
	if err := worker.Stop(preStopCtx); err != nil {
		preStopCancel()
		t.Fatalf("启动前 stop: %v", err)
	}
	preStopCancel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := worker.Start(ctx); err != nil {
		t.Fatalf("start worker: %v", err)
	}
	store.mu.Lock()
	if len(store.snapshots) != 1 || store.snapshots[0].Sequence != 1 {
		t.Fatalf("启动快照=%+v", store.snapshots)
	}
	store.mu.Unlock()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	if err := worker.Stop(stopCtx); err != nil {
		t.Fatalf("stop worker: %v", err)
	}
	if nodeLease.released != 1 {
		t.Fatalf("节点租约释放次数=%d want 1", nodeLease.released)
	}
	if err := worker.Stop(stopCtx); err != nil {
		t.Fatalf("重复 stop: %v", err)
	}
}

func TestWorkerRejectsDuplicateNodeIdentity(t *testing.T) {
	now := time.Date(2026, 7, 19, 4, 0, 0, 0, time.UTC)
	worker, err := NewWorker(WorkerConfig{
		Identity:  Identity{NodeID: "node-test-01", DisplayName: "测试节点", Source: IdentitySourceConfigured, Stable: true},
		Session:   Session{ID: validTestSnapshot(now).SessionID, StartedAt: now.Add(-time.Second)},
		Collector: &fakeCollector{collection: validCollection(now)},
		Store:     &fakeSnapshotStore{},
		NodeLease: &fakeLease{busy: true},
	})
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	if err := worker.Start(context.Background()); !errors.Is(err, ErrNodeIdentityInUse) {
		t.Fatalf("start err=%v want ErrNodeIdentityInUse", err)
	}
}

func TestWorkerFailsStartupWhenInitialWriteFails(t *testing.T) {
	now := time.Date(2026, 7, 19, 4, 0, 0, 0, time.UTC)
	lease := &fakeLease{}
	worker, err := NewWorker(WorkerConfig{
		Identity:  Identity{NodeID: "node-test-01", DisplayName: "测试节点", Source: IdentitySourceConfigured, Stable: true},
		Session:   Session{ID: validTestSnapshot(now).SessionID, StartedAt: now.Add(-time.Second)},
		Collector: &fakeCollector{collection: validCollection(now)},
		Store:     &fakeSnapshotStore{writeErr: errors.New("database down")},
		NodeLease: lease,
	})
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	if err := worker.Start(context.Background()); !errors.Is(err, ErrInitialSnapshotWrite) {
		t.Fatalf("start err=%v want ErrInitialSnapshotWrite", err)
	}
	if lease.released != 1 {
		t.Fatalf("首写失败后节点租约释放次数=%d want 1", lease.released)
	}
}

func validCollection(now time.Time) Collection {
	snapshot := validTestSnapshot(now)
	return Collection{
		CollectedAt:        now,
		ViewScope:          snapshot.ViewScope,
		CollectionStatus:   snapshot.CollectionStatus,
		ActiveErrorClasses: snapshot.ActiveErrorClasses,
		OSName:             snapshot.OSName,
		OSArch:             snapshot.OSArch,
		Metrics:            snapshot.Metrics,
		MetricStates:       snapshot.MetricStates,
	}
}
