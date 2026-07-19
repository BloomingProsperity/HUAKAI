package proxyhealth

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/workerlease"
)

// PROXY-04: 迟滞机制必须要求连续 N 次失败才标记为 dead、连续 M 次成功才恢复,
// 这样抖动的 proxy 永远不会来回振荡。
func TestDecideStatus_Hysteresis(t *testing.T) {
	c := &counters{}
	// active 在连续 deadThreshold 次失败之前一直保持 active
	for i := 1; i < deadThreshold; i++ {
		if got := decideStatus("active", false, c); got != "" {
			t.Fatalf("fail #%d should not flip yet, got %q", i, got)
		}
	}
	if got := decideStatus("active", false, c); got != "dead" {
		t.Fatalf("fail #%d should flip active->dead, got %q", deadThreshold, got)
	}

	// dead 在连续 recoverThreshold 次成功后恢复
	c2 := &counters{}
	for i := 1; i < recoverThreshold; i++ {
		if got := decideStatus("dead", true, c2); got != "" {
			t.Fatalf("success #%d should not recover yet, got %q", i, got)
		}
	}
	if got := decideStatus("dead", true, c2); got != "active" {
		t.Fatalf("success #%d should recover dead->active, got %q", recoverThreshold, got)
	}

	// 变异:防护。一次成功会重置失败计数器(反之亦然), 因此纯抖动永远不会触发
	// 状态转移。去掉计数器重置就会在这里翻车。
	c3 := &counters{}
	for i := 0; i < 6; i++ {
		if got := decideStatus("active", false, c3); got != "" {
			t.Fatalf("flap fail should not transition, got %q", got)
		}
		if got := decideStatus("active", true, c3); got != "" {
			t.Fatalf("flap success should not transition, got %q", got)
		}
	}
}

type fakeLister struct{ rows []ProxyTarget }

func (f fakeLister) List(context.Context) ([]ProxyTarget, error) { return f.rows, nil }

type fakeProber struct{ ok bool }

func (f fakeProber) Probe(context.Context, ProxyTarget) bool { return f.ok }

type fakeStore struct {
	touched []int64
	set     []string
}

func (f *fakeStore) Touch(_ context.Context, _ int64, id int64) error {
	f.touched = append(f.touched, id)
	return nil
}
func (f *fakeStore) SetStatus(_ context.Context, _, _ int64, status string) error {
	f.set = append(f.set, status)
	return nil
}

func TestWorker_Tick_FlipsDeadAfterThreshold(t *testing.T) {
	store := &fakeStore{}
	w := NewWorker(
		fakeLister{rows: []ProxyTarget{{ID: 1, TenantID: 9, Status: "active", Host: "h", Port: 1}}},
		fakeProber{ok: false}, store, time.Minute, nil)
	for i := 0; i < deadThreshold; i++ {
		w.tick(context.Background())
	}
	got := ""
	for _, s := range store.set {
		if s == "dead" {
			got = s
		}
	}
	if got != "dead" {
		t.Fatalf("expected a dead transition after %d failing ticks, set=%v", deadThreshold, store.set)
	}
}

func TestWorker_Tick_RecoversAfterThreshold(t *testing.T) {
	store := &fakeStore{}
	w := NewWorker(
		fakeLister{rows: []ProxyTarget{{ID: 2, TenantID: 9, Status: "dead", Host: "h", Port: 1}}},
		fakeProber{ok: true}, store, time.Minute, nil)
	for i := 0; i < recoverThreshold; i++ {
		w.tick(context.Background())
	}
	got := ""
	for _, s := range store.set {
		if s == "active" {
			got = s
		}
	}
	if got != "active" {
		t.Fatalf("expected an active recovery after %d ok ticks, set=%v", recoverThreshold, store.set)
	}
}

// 无状态转移 -> Touch 推进 last_check_at(这样「最久未检查优先」的排序能向前
// 推进, 该 proxy 也会被重新探测)。
func TestWorker_Tick_TouchesWhenNoChange(t *testing.T) {
	store := &fakeStore{}
	w := NewWorker(
		fakeLister{rows: []ProxyTarget{{ID: 3, TenantID: 9, Status: "active", Host: "h", Port: 1}}},
		fakeProber{ok: true}, store, time.Minute, nil)
	w.tick(context.Background())
	if len(store.touched) != 1 || store.touched[0] != 3 {
		t.Fatalf("expected Touch(3), got %v", store.touched)
	}
	if len(store.set) != 0 {
		t.Fatalf("expected no status change, got %v", store.set)
	}
}

func TestWorker_WaitBlocksUntilStartContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	w := NewWorker(fakeLister{}, fakeProber{}, &fakeStore{}, time.Hour, nil)
	w.Start(ctx)

	waitDone := make(chan error, 1)
	go func() {
		waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
		defer waitCancel()
		waitDone <- w.Wait(waitCtx)
	}()
	select {
	case err := <-waitDone:
		t.Fatalf("context 取消前 Wait 已返回: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("Wait: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("context 取消后 worker 未退出")
	}
}

type countingLister struct {
	rows  []ProxyTarget
	calls atomic.Int64
}

func (l *countingLister) List(context.Context) ([]ProxyTarget, error) {
	l.calls.Add(1)
	return l.rows, nil
}

type fakeLeaseSession struct {
	healthErr error
	released  atomic.Bool
}

func (s *fakeLeaseSession) Healthy(context.Context) error { return s.healthErr }
func (s *fakeLeaseSession) Release()                      { s.released.Store(true) }

type fakeSessionProvider struct {
	acquired bool
	session  *fakeLeaseSession
	err      error
}

func (p fakeSessionProvider) TryAcquireSession(context.Context) (bool, workerlease.Session, error) {
	return p.acquired, p.session, p.err
}

func TestWorker_LeaderLeaseAllowsOnlyHolderToProbe(t *testing.T) {
	lister := &countingLister{rows: []ProxyTarget{{ID: 11, TenantID: 7, Status: "active"}}}
	session := &fakeLeaseSession{}
	w := NewWorker(lister, fakeProber{ok: true}, &fakeStore{}, time.Hour, nil,
		WithLeaderLease(fakeSessionProvider{acquired: true, session: session}))
	ctx, cancel := context.WithCancel(context.Background())
	w.Start(ctx)

	deadline := time.Now().Add(time.Second)
	for lister.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if lister.calls.Load() != 1 {
		cancel()
		t.Fatalf("leader 取得租约后应立即且只探测一轮，calls=%d", lister.calls.Load())
	}
	cancel()
	if err := w.Wait(context.Background()); err != nil {
		t.Fatalf("等待 worker 退出: %v", err)
	}
	if !session.released.Load() {
		t.Fatal("worker 退出时必须释放 leader 租约")
	}
}

func TestWorker_LeaderLeaseFollowerDoesNotProbe(t *testing.T) {
	lister := &countingLister{}
	w := NewWorker(lister, fakeProber{ok: true}, &fakeStore{}, time.Hour, nil,
		WithLeaderLease(fakeSessionProvider{acquired: false}))
	ctx, cancel := context.WithCancel(context.Background())
	w.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := w.Wait(context.Background()); err != nil {
		t.Fatalf("等待 worker 退出: %v", err)
	}
	if got := lister.calls.Load(); got != 0 {
		t.Fatalf("未取得租约的副本不得探测，calls=%d", got)
	}
}

func TestWorker_LeaderLeaseUnhealthySessionStopsBeforeProbe(t *testing.T) {
	lister := &countingLister{}
	session := &fakeLeaseSession{healthErr: errors.New("连接已失效")}
	w := NewWorker(lister, fakeProber{ok: true}, &fakeStore{}, time.Hour, nil,
		WithLeaderLease(fakeSessionProvider{acquired: true, session: session}))
	ctx, cancel := context.WithCancel(context.Background())
	w.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := w.Wait(context.Background()); err != nil {
		t.Fatalf("等待 worker 退出: %v", err)
	}
	if got := lister.calls.Load(); got != 0 {
		t.Fatalf("租约会话失效后不得继续探测，calls=%d", got)
	}
	if !session.released.Load() {
		t.Fatal("失效租约必须释放后重新参与选主")
	}
}
