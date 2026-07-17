package codexagent

import (
	"testing"
	"time"
)

func TestBindingWaitBoundsPollingAndHonorsNearDeadline(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	far := now.Add(30 * time.Second)
	if got := bindingWait(taskBinding{LeaseExpiresAt: &far}, now); got != 250*time.Millisecond {
		t.Fatalf("far deadline wait=%s want 250ms", got)
	}
	near := now.Add(40 * time.Millisecond)
	if got := bindingWait(taskBinding{RetryAfter: &near}, now); got != 40*time.Millisecond {
		t.Fatalf("near deadline wait=%s want 40ms", got)
	}
}
