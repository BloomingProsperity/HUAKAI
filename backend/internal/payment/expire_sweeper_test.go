package payment

import (
	"context"
	"testing"
	"time"
)

// 守:后台过期扫描只允许把 expires_at < now 的 pending 单置 expired 并写 order_expired 审计。
// Mutation: 将 expires_at < now 写反为 expires_at > now → future pending 被过期、本测试红。
func TestMemoryStoreExpireStalePendingOrdersExpiresOnlyPastPendingAndAudits(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)
	future := now.Add(time.Minute)
	store := NewMemoryStore()
	store.orders[1] = &Order{ID: 1, TenantID: 7, UserID: 11, Status: StatusPending, ExpiresAt: &past, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}
	store.orders[2] = &Order{ID: 2, TenantID: 7, UserID: 11, Status: StatusPending, ExpiresAt: &future, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}
	store.orders[3] = &Order{ID: 3, TenantID: 7, UserID: 11, Status: StatusPaid, ExpiresAt: &past, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}

	expired, err := store.ExpireStalePendingOrders(ctx, now, 10)
	if err != nil {
		t.Fatalf("ExpireStalePendingOrders: %v", err)
	}
	if expired != 1 {
		t.Fatalf("expired=%d want 1", expired)
	}

	assertOrderStatus(t, store, 1, StatusExpired)
	assertOrderStatus(t, store, 2, StatusPending)
	assertOrderStatus(t, store, 3, StatusPaid)

	events, err := store.ListAuditEvents(ctx, 7, 1)
	if err != nil {
		t.Fatalf("ListAuditEvents expired order: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expired order audit events=%+v want exactly one event", events)
	}
	if events[0].EventType != AuditOrderExpired || events[0].ActorKind != ActorKindSystem {
		t.Fatalf("expired audit=%+v want order_expired/system", events[0])
	}
	for _, orderID := range []int64{2, 3} {
		events, err := store.ListAuditEvents(ctx, 7, orderID)
		if err != nil {
			t.Fatalf("ListAuditEvents order %d: %v", orderID, err)
		}
		if len(events) != 0 {
			t.Fatalf("order %d audit events=%+v want none", orderID, events)
		}
	}
}

// 守 BUG1:nil 检查必须在 w.mu.Lock() 之前。Mutation: lock-before-nil-guard → nil 解引用 panic。
func TestExpireSweeperNilReceiverStartStopDoesNotPanic(t *testing.T) {
	var w *ExpireSweeper
	w.Start(context.Background())
	w.Stop()
	if n, err := w.RunOnce(context.Background(), time.Now()); err != nil || n != 0 {
		t.Fatalf("nil RunOnce=(%d,%v), want (0,nil)", n, err)
	}
}

func assertOrderStatus(t *testing.T, store *MemoryStore, orderID int64, want OrderStatus) {
	t.Helper()
	got, err := store.GetOrder(context.Background(), 7, orderID)
	if err != nil {
		t.Fatalf("GetOrder %d: %v", orderID, err)
	}
	if got.Status != want {
		t.Fatalf("order %d status=%s want %s", orderID, got.Status, want)
	}
}
