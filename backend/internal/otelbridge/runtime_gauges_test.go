package otelbridge

import (
	"context"
	"testing"
)

// TestRuntimeGaugesBridgedToAlertSnapshot 验证实时进程运行时资源仪表盘
// (heap 分配字节数、goroutine 数、uptime 秒数)通过 ExpvarMetricSource 快照
// 暴露出来 —— 这正是 alert 引擎据以求值规则的那个指标 map —— 这样运维就能通过
// 现有的 alert-rule CRUD 对 gateway 自身的资源占用设阈值(F-GW-003 第 2 阶段)。
//
// 变异:从 bridgeCounters() 删除这三个运行时条目中的任一个 → 它的 key 在快照中缺失
// → 针对该 key 的断言变红。实时不变量(heap > 0、goroutines >= 1)无法被 map 的零值
// 满足,而 uptime 由一个显式的存在性检查守护(单凭它 >= 0 的下界,零值也会通过)。
func TestRuntimeGaugesBridgedToAlertSnapshot(t *testing.T) {
	snap, err := ExpvarMetricSource{}.Snapshot(context.Background(), 0)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	heap, ok := snap["huakai_runtime_heap_alloc_bytes"]
	if !ok || heap <= 0 {
		t.Errorf("huakai_runtime_heap_alloc_bytes present=%v value=%v; want present and >0 (live heap)", ok, heap)
	}

	goroutines, ok := snap["huakai_runtime_goroutines"]
	if !ok || goroutines < 1 {
		t.Errorf("huakai_runtime_goroutines present=%v value=%v; want present and >=1 (the test goroutine alone)", ok, goroutines)
	}

	if _, ok := snap["huakai_runtime_uptime_seconds"]; !ok {
		t.Error("huakai_runtime_uptime_seconds absent from snapshot; want present (uptime gauge missing from bridge)")
	}
}
