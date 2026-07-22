//go:build integration_pg

package hermes

import (
	"context"
	"fmt"
	"testing"
	"time"

	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
)

func TestMessageRetentionRealPG(t *testing.T) {
	pool := openHermesIntegrationPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	now := time.Now().UTC().Truncate(time.Microsecond)
	var tenantID, userID, oldConversationID, recentConversationID int64
	name := fmt.Sprintf("hermes-retention-%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, name).Scan(&tenantID); err != nil {
		t.Fatalf("创建测试租户: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (
		tenant_id, email, display_name, status, role, principal_kind
	) VALUES ($1, $2, '保留期测试管理员', 'active', 'admin', 'human') RETURNING id`,
		tenantID, name+"@example.invalid").Scan(&userID); err != nil {
		t.Fatalf("创建测试用户: %v", err)
	}
	insertConversation := func(createdAt time.Time) int64 {
		var id int64
		if err := pool.QueryRow(ctx, `INSERT INTO hermes_conversations (
			tenant_id, owner_user_id, title, created_at, updated_at, last_message_at,
			actor_source, actor_id, actor_role
		) VALUES ($1, $2, '保留期测试', $3, $3, $3, 'session', $2, 'tenant_operator')
		RETURNING id`, tenantID, userID, createdAt).Scan(&id); err != nil {
			t.Fatalf("创建测试会话: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO hermes_messages (
			tenant_id, conversation_id, role, content, created_at
		) VALUES ($1, $2, 'user', '{"type":"text","text":"redacted"}'::jsonb, $3)`,
			tenantID, id, createdAt); err != nil {
			t.Fatalf("创建测试消息: %v", err)
		}
		return id
	}
	oldConversationID = insertConversation(now.AddDate(0, 0, -31))
	recentConversationID = insertConversation(now.AddDate(0, 0, -30))
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM hermes_messages WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM hermes_conversations WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1`, userID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})

	store := NewPostgresMessagePurgeStore(dbhermes.New(pool))
	lockTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("开启租约事务: %v", err)
	}
	if _, err := lockTx.Exec(ctx, `SELECT pg_advisory_xact_lock(
		hashtextextended('huakai.hermes.message-retention', 0))`); err != nil {
		_ = lockTx.Rollback(ctx)
		t.Fatalf("占用消息清理租约: %v", err)
	}
	deletedWhileLocked, err := store.PurgeMessagesBefore(ctx, now.AddDate(0, 0, -30), 100)
	if err != nil {
		_ = lockTx.Rollback(ctx)
		t.Fatalf("租约冲突时清理消息: %v", err)
	}
	if deletedWhileLocked != 0 {
		_ = lockTx.Rollback(ctx)
		t.Fatalf("租约被占用时删除了 %d 条消息", deletedWhileLocked)
	}
	if err := lockTx.Rollback(ctx); err != nil {
		t.Fatalf("释放消息清理租约: %v", err)
	}

	worker := NewMessageRetentionWorker(MessageRetentionWorkerConfig{
		Store: store, RetentionDays: DefaultMessageRetentionDays, BatchLimit: 1,
	})
	deleted, err := worker.RunOnce(ctx, now)
	if err != nil {
		t.Fatalf("执行保留期清理: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("删除数量=%d，期望旧消息和空会话各一条", deleted)
	}
	var oldCount, recentCount int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM hermes_conversations WHERE id=$1),
		(SELECT count(*) FROM hermes_conversations WHERE id=$2)`,
		oldConversationID, recentConversationID,
	).Scan(&oldCount, &recentCount); err != nil {
		t.Fatalf("检查会话保留结果: %v", err)
	}
	if oldCount != 0 || recentCount != 1 {
		t.Fatalf("会话保留结果=(旧:%d,边界:%d)，期望 (0,1)", oldCount, recentCount)
	}
}
