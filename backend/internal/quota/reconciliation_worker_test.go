package quota

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// countingSweepStore 是极简 fake:只覆盖 ListTenantsWithDueReconciliationJobs 记录调用次数
// 并返回空租户(→ReconcileAllTenants total=0、不进 ReconcileDueJobs,故嵌入的 PGStore 其它
// 方法不被调用,可安全留 nil)。用于驱动真正的 Start→loop 后台路径而不碰真 PG。
type countingSweepStore struct {
	PGStore
	calls *atomic.Int32
}

func (s countingSweepStore) ListTenantsWithDueReconciliationJobs(context.Context, time.Time, int) ([]int64, error) {
	s.calls.Add(1)
	return nil, nil
}

// RunOnce 的清扫段也会打到 store;返回空让轮次保持空转语义。
func (s countingSweepStore) ListStaleReservedReservations(context.Context, time.Time, int) ([]StaleReservation, error) {
	return nil, nil
}

// TestReconciliationWorker_NilReconcilerSafe:reconciler 为 nil 时全部空操作、不 panic
//(生产 knob 关时不构造 reconciler,worker 相关 stop hook 也不会被设——但防御式仍要安全)。
func TestReconciliationWorker_NilReconcilerSafe(t *testing.T) {
	w := NewReconciliationWorker(nil, 0)
	w.Start(context.Background()) // nil reconciler → 不启 goroutine
	n, err := w.RunOnce(context.Background(), time.Time{})
	if n != 0 || err != nil {
		t.Fatalf("nil reconciler RunOnce=%d,%v; want 0,nil", n, err)
	}
	w.Stop() // 未真启动,Stop 应安全
}

// TestReconciliationWorker_StartLoopStopDrivesSweep 用真 reconciler(fake store 计数)+ 极短
// interval 驱动**真正的后台路径** Start→loop→ticker→RunOnce→Stop,守护生产接线入口
//(wiring.go 用的正是 Start+Stop,而非测试直接调 RunOnce)。断言:
//   (a) loop 真跑:短 interval 下 sweep 被调 ≥1 次;
//   (b) Stop 后协程确实退出:Stop 迅速返回(close(w.stop)→loop 退→<-done),且 Stop 后不再 sweep;
//   (c) 双 Start 不重复起协程(否则 Stop 只 close 一个 stop、另一协程泄漏→Stop 后 sweep 仍增长)。
//
// §14 变异契约:①Stop 删 close(w.stop)/<-done → loop 不退,Stop 的 <-done 永久阻塞 → 断言(b)
// 的 2s 超时红;②Start 破坏 running 守卫/去掉 goroutine → 断言(a) sweep=0 红;③双 Start 重复起
// 协程(去掉 running 守卫)→ Stop 后 sweep 仍增长 → 断言(c)红。
func TestReconciliationWorker_StartLoopStopDrivesSweep(t *testing.T) {
	var calls atomic.Int32
	store := countingSweepStore{calls: &calls}
	reconciler := NewReconciler(nil, store, ReconcilerOptions{})
	worker := NewReconciliationWorker(reconciler, 5*time.Millisecond)
	ctx := context.Background()

	worker.Start(ctx)
	worker.Start(ctx) // 双 Start:应幂等,不重复起协程

	// (a) 等 loop 至少驱动一次 sweep。
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if calls.Load() == 0 {
		t.Fatal("loop 未驱动 sweep(Start/ticker 未真跑)")
	}

	// (b) Stop 必须迅速返回(协程退出);超时=loop 没退、<-done 阻塞。
	done := make(chan struct{})
	go func() { worker.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop 未在期限内返回——协程未退出(close(stop)/<-done 缺失)")
	}

	// (b)+(c) Stop 后 sweep 不再增长(协程真停 + 无泄漏的第二协程)。
	after := calls.Load()
	time.Sleep(40 * time.Millisecond) // 远大于 5ms interval,若协程仍活必增长
	if got := calls.Load(); got != after {
		t.Fatalf("Stop 后 sweep 仍在跑:%d→%d(协程未退或双 Start 泄漏了第二协程)", after, got)
	}
}

// TestReconciliationWorker_DefaultInterval:interval<=0 落默认分钟级(避免 0 间隔 ticker panic)。
func TestReconciliationWorker_DefaultInterval(t *testing.T) {
	w := NewReconciliationWorker(nil, 0)
	if w.interval != defaultReconciliationWorkerInterval {
		t.Fatalf("interval=%v; want default %v", w.interval, defaultReconciliationWorkerInterval)
	}
	w2 := NewReconciliationWorker(nil, 3*time.Second)
	if w2.interval != 3*time.Second {
		t.Fatalf("interval=%v; want 3s(显式值不被覆盖)", w2.interval)
	}
}
