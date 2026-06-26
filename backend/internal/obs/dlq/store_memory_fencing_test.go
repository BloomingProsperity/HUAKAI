package dlq

import (
	"context"
	"errors"
	"testing"
	"time"
)

// 守 wave-2 P2(DLQ owner 围栏): worker A claim 后 visibility 超时被 worker B 重领, A 迟到的
// MarkCompleted 必须被拒(owner 不符), 不得把 B 正在处理的事件覆盖成 completed。
// Mutation: 去掉 update()/MarkFailedDead 的 owner 围栏 -> A 的 mark 成功 -> 事件被 stale A
// 标 completed -> 断言红。
func TestMemoryOutbox_StaleOwnerCannotClobberReclaimed(t *testing.T) {
	ctx := context.Background()
	box := NewMemoryOutbox()
	ev, err := box.Enqueue(ctx, OutboxEvent{TenantID: 7, EventType: EventTypeChannelAlert, Payload: []byte(`{"k":"v"}`)})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	base := ev.NextRetryAt
	// worker A 领取,visibility=1m
	evA, ok, err := box.Dequeue(ctx, DequeueOptions{Priority: PriorityAny, Now: base, WorkerID: "workerA", VisibilityTimeout: time.Minute})
	if err != nil || !ok || evA.ID != ev.ID {
		t.Fatalf("A dequeue: ok=%v id=%s err=%v", ok, evA.ID, err)
	}
	// 超时后 worker B 重领同一事件
	evB, ok, err := box.Dequeue(ctx, DequeueOptions{Priority: PriorityAny, Now: base.Add(2 * time.Minute), WorkerID: "workerB", VisibilityTimeout: time.Minute})
	if err != nil || !ok || evB.ID != ev.ID {
		t.Fatalf("B re-dequeue: ok=%v id=%s err=%v", ok, evB.ID, err)
	}
	// stale worker A 迟到 MarkCompleted -> 必须被 owner 围栏拒绝
	if err := box.MarkCompleted(ctx, ev.ID, "workerA"); !errors.Is(err, ErrEventNotFound) {
		t.Fatalf("stale A MarkCompleted err=%v want ErrEventNotFound (lease lost)", err)
	}
	// 事件必须仍是 processing(归 B), 不是 completed
	for _, item := range box.Snapshot() {
		if item.ID == ev.ID && item.Status != StatusProcessing {
			t.Fatalf("event status=%s want processing (stale A must not complete it)", item.Status)
		}
	}
	// 当前 owner B MarkCompleted -> 成功
	if err := box.MarkCompleted(ctx, ev.ID, "workerB"); err != nil {
		t.Fatalf("owner B MarkCompleted: %v", err)
	}
	for _, item := range box.Snapshot() {
		if item.ID == ev.ID && item.Status != StatusCompleted {
			t.Fatalf("after B complete status=%s want completed", item.Status)
		}
	}
}
