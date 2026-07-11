package settlementintent

import (
	"context"
	"testing"
	"time"
)

// TestAfterDeliveryAsyncDisabledHasZeroHotPathAllocation 守住默认关闭时直接复用
// no-op 回调，不为每次流式首帧创建通道、闭包或异步任务。
func TestAfterDeliveryAsyncDisabledHasZeroHotPathAllocation(t *testing.T) {
	tests := []struct {
		name    string
		tracker *Tracker
	}{
		{name: "nil_tracker"},
		{name: "nil_store", tracker: NewTracker(nil)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			allocations := testing.AllocsPerRun(1000, func() {
				after, wait := tc.tracker.AfterDeliveryAsync(context.Background())
				after(time.Time{})
				wait()
			})
			if allocations != 0 {
				t.Fatalf("默认关闭的首帧旁路 allocations=%v want 0", allocations)
			}
		})
	}
}
