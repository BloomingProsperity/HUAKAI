// pasr_aging_worker_test.go — PASR-lite A5 worker 单测。
package pool

import (
	"context"
	"testing"
	"time"
)

func TestPASRAgingWorker_Defaults(t *testing.T) {
	w := NewPASRAgingWorker(PASRAgingWorkerConfig{Segments: NewSegmentTable(SegmentTableConfig{})})
	if w.interval != DefaultAgingInterval {
		t.Errorf("默认 interval=%v want %v", w.interval, DefaultAgingInterval)
	}
	if w.now == nil {
		t.Error("默认 now 应非 nil")
	}
}

func TestPASRAgingWorker_TickOnce_EvictsExpired(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	clock := now
	tbl := NewSegmentTable(SegmentTableConfig{
		MaxAge: 30 * time.Minute,
		Now:    func() time.Time { return clock },
	})
	ring := NewAccountRing([]int64{10, 20, 30}, 1)

	tbl.LookupOrCreate(1, []byte("old"), ring)   // 创建时 LastReadAt=12:00
	clock = clock.Add(40 * time.Minute)          // 12:40
	tbl.LookupOrCreate(1, []byte("fresh"), ring) // LastReadAt=12:40

	w := NewPASRAgingWorker(PASRAgingWorkerConfig{
		Segments: tbl,
		Now:      func() time.Time { return clock },
	})
	w.TickOnce()

	if w.TickCount() != 1 {
		t.Errorf("TickCount=%d want 1", w.TickCount())
	}
	if w.EvictedTotal() != 1 {
		t.Errorf("EvictedTotal=%d want 1", w.EvictedTotal())
	}
	if tbl.Lookup(1, []byte("old")) != nil {
		t.Error("old 段应被 evict")
	}
	if tbl.Lookup(1, []byte("fresh")) == nil {
		t.Error("fresh 段应保留")
	}
}

func TestPASRAgingWorker_StartStop_Lifecycle(t *testing.T) {
	tbl := NewSegmentTable(SegmentTableConfig{})
	w := NewPASRAgingWorker(PASRAgingWorkerConfig{
		Segments: tbl,
		Interval: 50 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w.Start(ctx)
	time.Sleep(180 * time.Millisecond) // 应触发 ~3 次 tick
	w.Stop()

	if w.TickCount() < 2 {
		t.Errorf("180ms 内应至少 tick 2 次, 实际 %d", w.TickCount())
	}
	// Stop 后再 Start 应可重新跑
	w.Start(ctx)
	preTicks := w.TickCount()
	time.Sleep(120 * time.Millisecond)
	w.Stop()
	if w.TickCount() <= preTicks {
		t.Error("第二次 Start 后应继续 tick")
	}
}

func TestPASRAgingWorker_Idempotent_StartStop(t *testing.T) {
	tbl := NewSegmentTable(SegmentTableConfig{})
	w := NewPASRAgingWorker(PASRAgingWorkerConfig{
		Segments: tbl,
		Interval: 50 * time.Millisecond,
	})
	ctx := context.Background()

	w.Start(ctx)
	w.Start(ctx) // 重复 Start 应 no-op
	w.Stop()
	w.Stop() // 重复 Stop 应 no-op

	// 不 panic 即通过
}

func TestPASRAgingWorker_ContextCancellation_StopsWorker(t *testing.T) {
	tbl := NewSegmentTable(SegmentTableConfig{})
	w := NewPASRAgingWorker(PASRAgingWorkerConfig{
		Segments: tbl,
		Interval: 50 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	w.Start(ctx)
	time.Sleep(80 * time.Millisecond)
	cancel() // ctx done → loop 退出
	time.Sleep(80 * time.Millisecond)

	preStop := w.TickCount()
	time.Sleep(150 * time.Millisecond)
	if w.TickCount() != preStop {
		t.Errorf("ctx cancel 后不应继续 tick: pre=%d post=%d", preStop, w.TickCount())
	}
}

func TestPASRAgingWorker_NilSegments_NoOp(t *testing.T) {
	w := NewPASRAgingWorker(PASRAgingWorkerConfig{Segments: nil})
	ctx := context.Background()
	w.Start(ctx) // 不应 panic, 不应启动 loop
	if w.TickCount() != 0 {
		t.Error("nil segments 时不应 tick")
	}
}
