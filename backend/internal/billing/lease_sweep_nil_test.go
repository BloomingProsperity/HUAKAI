package billing

import (
	"context"
	"testing"
)

// 守 BUG1:nil 检查必须在 s.mu.Lock() 之前。Mutation: lock-before-nil-guard → nil 解引用 panic
// → 本测试 panic 失败。
func TestLeaseSweeper_NilReceiverStartStopDoesNotPanic(t *testing.T) {
	var s *LeaseSweeper // nil receiver
	s.Start(context.Background())
	s.Stop()
	if n, err := s.SweepOnce(context.Background()); err != nil || n != 0 {
		t.Fatalf("nil SweepOnce = (%d,%v), want (0,nil)", n, err)
	}
}
