package eventbus

import (
	"fmt"
	"testing"
	"time"
)

// TestSetStateErrorCodeEvictsOldestBeyondCap 证明每个 handler 的状态账本受
// Config.MaxStates 限制:写入数量超过上限的不同事件 ID 后,map 大小始终不
// 超过上限,且最近更新的条目仍可通过 State() 查询,而最旧的条目被淘汰。
//
// 捕获的回归(一句话):若移除 setStateErrorCode 中限上限并淘汰的分支,
// b.states 会无限增长,使 len(b.states) 超过上限,此测试将在大小断言处失败。
func TestSetStateErrorCodeEvictsOldestBeyondCap(t *testing.T) {
	const cap = 4
	const writes = 12

	// 使用确定且严格递增的时钟,使 UpdatedAt 的先后顺序毫无歧义:第 N 次
	// 写入总是比第 (N-1) 次更新。
	tick := time.Unix(0, 0).UTC()
	bus := New(Config{MaxStates: cap}, WithClock(func() time.Time {
		tick = tick.Add(time.Second)
		return tick
	}))

	const handlerID HandlerID = "metrics_aggregator"
	for i := 0; i < writes; i++ {
		ev := RequestCompletionEvent{ID: fmt.Sprintf("evt-%02d", i), TenantID: 1}
		bus.setStateErrorCode(ev, handlerID, HandlerStateDone, "")
	}

	bus.mu.RLock()
	got := len(bus.states)
	bus.mu.RUnlock()
	if got > cap {
		t.Fatalf("ledger not bounded: len(states)=%d, want <= cap=%d", got, cap)
	}

	// 最后 `cap` 次写入是最近的,必须得以保留。
	for i := writes - cap; i < writes; i++ {
		id := fmt.Sprintf("evt-%02d", i)
		if _, ok := bus.State(id, handlerID); !ok {
			t.Fatalf("most-recent entry %q was evicted but should be retained", id)
		}
	}

	// 第一次写入是最旧的,必定已被淘汰。
	if _, ok := bus.State("evt-00", handlerID); ok {
		t.Fatalf("oldest entry evt-00 should have been evicted but is still present")
	}
}

// TestSetStateErrorCodeUnboundedWhenCapNonPositive 验证应急出口:MaxStates <= 0
// 时账本不设上限,使运维可恢复历史行为。捕获的回归:若 NormalizeConfig(或
// 淘汰分支)把非正的上限强制转换为有限值,此测试将失败,因为超出该假定上限
// 的条目会被丢弃。
func TestSetStateErrorCodeUnboundedWhenCapNonPositive(t *testing.T) {
	const writes = 50
	bus := New(Config{MaxStates: 0})

	const handlerID HandlerID = "metrics_aggregator"
	for i := 0; i < writes; i++ {
		ev := RequestCompletionEvent{ID: fmt.Sprintf("evt-%02d", i), TenantID: 1}
		bus.setStateErrorCode(ev, handlerID, HandlerStateDone, "")
	}

	bus.mu.RLock()
	got := len(bus.states)
	bus.mu.RUnlock()
	if got != writes {
		t.Fatalf("unbounded ledger lost entries: len(states)=%d, want %d", got, writes)
	}
}
