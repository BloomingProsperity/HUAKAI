package settlementintent

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestMarkDeliveringDisabledHasZeroHotPathAllocation 守住默认关闭时直接返回，
// 不为每次响应创建通道、闭包或异步任务。
func TestMarkDeliveringDisabledHasZeroHotPathAllocation(t *testing.T) {
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
				if err := tc.tracker.MarkDelivering(context.Background(), time.Time{}); err != nil {
					t.Fatalf("默认关闭不应返回错误: %v", err)
				}
			})
			if allocations != 0 {
				t.Fatalf("默认关闭的交付硬门 allocations=%v want 0", allocations)
			}
		})
	}
}

func TestMarkDeliveringEnabledWithoutPendingFailsClosed(t *testing.T) {
	tracker := NewTracker(&fakeTrackerStore{})
	err := tracker.MarkDelivering(context.Background(), time.Now())
	if !errors.Is(err, ErrDeliveryEvidenceUnavailable) {
		t.Fatalf("err=%v want ErrDeliveryEvidenceUnavailable", err)
	}
}

type fakeTrackerStore struct {
	Store
}
