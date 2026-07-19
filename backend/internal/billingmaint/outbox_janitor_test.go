package billingmaint

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakePruneStore 记录每次调用并按预设序列返回删除行数。
type fakePruneStore struct {
	returns []int64
	errAt   int
	calls   int
	limits  []int64
}

func (s *fakePruneStore) PruneBatch(_ context.Context, limit int64) (int64, error) {
	idx := s.calls
	s.calls++
	s.limits = append(s.limits, limit)
	if s.errAt > 0 && idx == s.errAt-1 {
		return 0, errors.New("db 抖动")
	}
	if idx < len(s.returns) {
		return s.returns[idx], nil
	}
	return 0, nil
}

// TestSweepOnceDrainsBacklogInBatches 锁定「按批排空」:删满一批(=limit)说明还有积压
// 必须继续下一批,直到不足一批停。变异:sweepOnce 删掉 drain 循环只删一批 → 积压场景
// 只调 1 次 → 本断言 RED(首次启用面对历史积压时永远追不平)。
func TestSweepOnceDrainsBacklogInBatches(t *testing.T) {
	store := &fakePruneStore{returns: []int64{5, 5, 3}}
	j := NewSchedulerOutboxJanitor(store, time.Hour, 5)
	j.stop = make(chan struct{})
	j.sweepOnce(context.Background())
	if store.calls != 3 {
		t.Fatalf("按批排空应调 3 次(5,5,3),实调 %d 次", store.calls)
	}
	for i, limit := range store.limits {
		if limit != 5 {
			t.Fatalf("第 %d 批 limit 应为 5,实 %d", i+1, limit)
		}
	}
}

// TestSweepOnceStopsOnError 锁定 best-effort:DB 报错本轮停(等下个 tick),不无限重试。
// 变异:忽略 err 继续循环 → 报错后仍继续调 → 调用数超 1 → RED。
func TestSweepOnceStopsOnError(t *testing.T) {
	store := &fakePruneStore{returns: []int64{5, 5}, errAt: 1}
	j := NewSchedulerOutboxJanitor(store, time.Hour, 5)
	j.stop = make(chan struct{})
	j.sweepOnce(context.Background())
	if store.calls != 1 {
		t.Fatalf("首批报错应停止本轮,实调 %d 次", store.calls)
	}
}

// TestSweepOnceHonorsCancellation 锁定长积压排空可被取消打断:ctx 取消后不再发起新批。
// 变异:删批间取消检查 → 取消后仍按序列继续调 → RED。
func TestSweepOnceHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &fakePruneStore{returns: []int64{5, 5, 5}}
	j := NewSchedulerOutboxJanitor(store, time.Hour, 5)
	j.stop = make(chan struct{})
	j.sweepOnce(ctx)
	if store.calls != 0 {
		t.Fatalf("已取消的 ctx 不应再发起修剪,实调 %d 次", store.calls)
	}
}

// TestJanitorStartStopLifecycle 锁定 Start/Stop 幂等与优雅退出(镜像 ReplayJanitor 语义)。
func TestJanitorStartStopLifecycle(t *testing.T) {
	store := &fakePruneStore{}
	j := NewSchedulerOutboxJanitor(store, time.Hour, 0)
	ctx := context.Background()
	j.Start(ctx)
	j.Start(ctx) // 重复 Start no-op
	done := make(chan struct{})
	go func() {
		j.Stop()
		j.Stop() // 重复 Stop no-op
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop 未在 2s 内优雅退出")
	}
}
