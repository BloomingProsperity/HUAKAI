package usernotice

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBroadcastValidation(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore(), WithClock(func() time.Time { return now }))

	tests := []struct {
		name string
		in   BroadcastInput
	}{
		{
			name: "empty title",
			in:   BroadcastInput{TenantID: 7, Title: " ", Body: "body", Severity: SeverityInfo},
		},
		{
			name: "empty body",
			in:   BroadcastInput{TenantID: 7, Title: "title", Body: "\t", Severity: SeverityInfo},
		},
		{
			name: "bad severity",
			in:   BroadcastInput{TenantID: 7, Title: "title", Body: "body", Severity: Severity("emergency")},
		},
		{
			name: "missing tenant",
			in:   BroadcastInput{Title: "title", Body: "body", Severity: SeverityInfo},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 变异：移除 title/body/severity/tenant 校验；这条 broadcast 会成功。
			if _, err := svc.Broadcast(context.Background(), tt.in); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Broadcast err=%v want ErrInvalidInput", err)
			}
		})
	}
}

func TestListNotificationsSelfScopedUnreadFirst(t *testing.T) {
	// 变异：在 ListForUser 中去掉 user_id 过滤或 unread_only 过滤；用户 A 会看到用户 B 的行，或在仅未读模式下看到已读行。
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	store.AddActiveUser(7, 101)
	store.AddActiveUser(7, 202)
	svc := NewService(store, WithClock(func() time.Time { return now }))

	if _, err := svc.Broadcast(context.Background(), BroadcastInput{TenantID: 7, Title: "old", Body: "body"}); err != nil {
		t.Fatalf("Broadcast old: %v", err)
	}
	oldForA := mustListUserNotices(t, svc, 7, 101, false)
	if len(oldForA) != 1 {
		t.Fatalf("oldForA len=%d want 1", len(oldForA))
	}
	if _, err := svc.MarkRead(context.Background(), MarkReadInput{TenantID: 7, UserID: 101, ID: oldForA[0].ID}); err != nil {
		t.Fatalf("MarkRead old A: %v", err)
	}
	now = now.Add(time.Minute)
	if _, err := svc.Broadcast(context.Background(), BroadcastInput{TenantID: 7, Title: "new", Body: "body", Severity: SeverityWarning}); err != nil {
		t.Fatalf("Broadcast new: %v", err)
	}

	items, err := svc.ListForUser(context.Background(), ListInput{TenantID: 7, UserID: 101, Limit: 50})
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items=%+v want 2 rows for user A only", items)
	}
	if items[0].Title != "new" || items[0].ReadAt != nil || items[1].Title != "old" || items[1].ReadAt == nil {
		t.Fatalf("items order/read state=%+v want new unread then old read", items)
	}

	unread, err := svc.ListForUser(context.Background(), ListInput{TenantID: 7, UserID: 101, UnreadOnly: true, Limit: 50})
	if err != nil {
		t.Fatalf("ListForUser unread: %v", err)
	}
	if len(unread) != 1 || unread[0].Title != "new" || unread[0].UserID != 101 {
		t.Fatalf("unread=%+v want only user A's new unread row", unread)
	}
}

func TestMarkReadOwnOnly(t *testing.T) {
	// 变异：从 MarkRead 更新中去掉 user_id；用户 A 会把用户 B 的通知标记为已读。
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	store.AddActiveUser(7, 101)
	store.AddActiveUser(7, 202)
	svc := NewService(store, WithClock(func() time.Time { return now }))

	if _, err := svc.Broadcast(context.Background(), BroadcastInput{TenantID: 7, Title: "tenant notice", Body: "body"}); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	rowB := mustListUserNotices(t, svc, 7, 202, true)[0]
	if _, err := svc.MarkRead(context.Background(), MarkReadInput{TenantID: 7, UserID: 101, ID: rowB.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("MarkRead cross-user err=%v want ErrNotFound", err)
	}
	countB, err := svc.UnreadCount(context.Background(), 7, 202)
	if err != nil {
		t.Fatalf("UnreadCount B: %v", err)
	}
	if countB != 1 {
		t.Fatalf("user B unread count=%d want 1 after user A cross-read attempt", countB)
	}

	rowA := mustListUserNotices(t, svc, 7, 101, true)[0]
	if _, err := svc.MarkRead(context.Background(), MarkReadInput{TenantID: 7, UserID: 101, ID: rowA.ID}); err != nil {
		t.Fatalf("MarkRead own: %v", err)
	}
	countA, err := svc.UnreadCount(context.Background(), 7, 101)
	if err != nil {
		t.Fatalf("UnreadCount A: %v", err)
	}
	if countA != 0 {
		t.Fatalf("user A unread count=%d want 0 after own read", countA)
	}
}

func mustListUserNotices(t *testing.T, svc *Service, tenantID, userID int64, unreadOnly bool) []Notification {
	t.Helper()
	items, err := svc.ListForUser(context.Background(), ListInput{
		TenantID:   tenantID,
		UserID:     userID,
		UnreadOnly: unreadOnly,
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("ListForUser tenant=%d user=%d unread=%v: %v", tenantID, userID, unreadOnly, err)
	}
	return items
}
