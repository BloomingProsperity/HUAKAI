package quota

import (
	"context"
	"testing"
	"time"
)

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

// TestReconciliationWorker_StopBeforeStartSafe + 双 Start 幂等:生命周期不 panic、不重复起
// goroutine。用 nil reconciler 避免碰 PG(本用例只验生命周期骨架,真 sweep 由集成测试覆盖)。
func TestReconciliationWorker_LifecycleIdempotent(t *testing.T) {
	w := NewReconciliationWorker(nil, time.Minute)
	w.Stop()                       // start 前 stop:安全
	w.Start(context.Background())  // nil reconciler:no-op
	w.Start(context.Background())  // 双 start:幂等
	w.Stop()
	w.Stop() // 双 stop:幂等
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
