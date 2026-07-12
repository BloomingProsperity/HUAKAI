package queuewait

import "testing"

func TestTracker_AllowsUpToMaxWaitingAndDeletesAtZero(t *testing.T) {
	tracker := NewTracker()
	key := Key{TenantID: 7, PoolGroupID: 42, AccountID: 101}

	release1, ok := tracker.TryAcquire(key, 2)
	if !ok {
		t.Fatal("第一个等待位应被允许")
	}
	release2, ok := tracker.TryAcquire(key, 2)
	if !ok {
		t.Fatal("第二个等待位应被允许")
	}
	if got := tracker.Depth(key); got != 2 {
		t.Fatalf("depth=%d want 2", got)
	}
	if _, ok := tracker.TryAcquire(key, 2); ok {
		t.Fatal("第三个等待位应被 MaxWaiting=2 拒绝")
	}

	release1()
	release3, ok := tracker.TryAcquire(key, 2)
	if !ok {
		t.Fatal("释放一个等待位后应允许新等待者进入")
	}
	release2()
	release3()
	if got := tracker.Depth(key); got != 0 {
		t.Fatalf("depth=%d want 0；计数归零必须删除 key", got)
	}
}

func TestTracker_MaxWaitingNonPositiveRejectsImmediately(t *testing.T) {
	tracker := NewTracker()
	key := Key{TenantID: 7, PoolGroupID: 42, AccountID: 101}

	if _, ok := tracker.TryAcquire(key, 0); ok {
		t.Fatal("MaxWaiting=0 表示零等待位，第一名等待者也应溢出")
	}
	if _, ok := tracker.TryAcquire(key, -1); ok {
		t.Fatal("MaxWaiting<0 也应按零等待位处理")
	}
	if got := tracker.Depth(key); got != 0 {
		t.Fatalf("depth=%d want 0", got)
	}
}
