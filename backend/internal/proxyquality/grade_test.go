package proxyquality

import (
	"testing"
	"time"
)

func TestGradeSeparatesReachabilityFromLatency(t *testing.T) {
	if Grade(false, 10) != GradePoor {
		t.Fatal("失败必须是差档，不能因低延迟冒充优质")
	}
	if Grade(true, 100) != GradeExcellent {
		t.Fatal("打通且低延迟应为优")
	}
	if Grade(true, 800) != GradeGood {
		t.Fatal("打通且中延迟应为良")
	}
	if Grade(true, 2000) != GradeDegraded {
		t.Fatal("打通且高延迟应为降级")
	}
	if Grade(true, 4000) != GradePoor {
		t.Fatal("打通但过慢仍是差档")
	}
}

func TestEffectiveExpiresStoredExcellent(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	freshAt := now.Add(-14 * time.Minute)
	staleAt := now.Add(-16 * time.Minute)
	if got := Effective(GradeExcellent, freshAt, now); got != GradeExcellent {
		t.Fatalf("窗内应保留存档档 got=%s", got)
	}
	if got := Effective(GradeExcellent, staleAt, now); got != GradeUnknown {
		t.Fatalf("过期优质必须变成未知 got=%s", got)
	}
	if Fresh(time.Time{}, now) {
		t.Fatal("从未探测不得算新鲜")
	}
}
