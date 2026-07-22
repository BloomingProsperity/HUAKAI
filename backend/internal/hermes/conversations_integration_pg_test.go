//go:build integration_pg

package hermes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesprincipal"
)

func TestConversationActorIsolationRealPG(t *testing.T) {
	pool := openHermesIntegrationPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	name := fmt.Sprintf("hermes-actor-isolation-%d", time.Now().UnixNano())
	var tenantID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO tenants (name) VALUES ($1) RETURNING id`, name).Scan(&tenantID); err != nil {
		t.Fatalf("创建测试租户: %v", err)
	}
	principal, err := hermesprincipal.NewStore(pool).Ensure(ctx, tenantID)
	if err != nil {
		t.Fatalf("创建 Hermes 服务主体: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM hermes_audit_events WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM hermes_messages WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM hermes_conversations WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM hermes_settings WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM hermes_api_profiles WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM hermes_service_principals WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM api_keys WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})

	queries := dbhermes.New(pool)
	create := func(actorID int64, title string) int64 {
		id, createErr := queries.CreateConversation(ctx, dbhermes.CreateConversationParams{
			TenantID: tenantID, OwnerUserID: principal.UserID,
			ActorSource: "session", ActorID: actorID,
			ActorRole: "tenant_operator", Title: &title,
		})
		if createErr != nil {
			t.Fatalf("创建管理员 %d 的会话: %v", actorID, createErr)
		}
		return id
	}
	conversationA := create(101, "管理员 A")
	conversationB := create(202, "管理员 B")
	content := json.RawMessage(`{"type":"text","text":"仅管理员 B 可见"}`)
	if _, err := queries.AppendMessage(ctx, dbhermes.AppendMessageParams{
		TenantID: tenantID, ConversationID: conversationB, OwnerUserID: principal.UserID,
		ActorSource: "session", ActorID: 202, Role: "user", Content: content,
	}); err != nil {
		t.Fatalf("写入管理员 B 的消息: %v", err)
	}

	service := NewServiceWithTx(queries, pool)
	rows, err := service.ListConversationsByOwner(ctx, tenantID, principal.UserID, "session", 101, 50, 0)
	if err != nil {
		t.Fatalf("列出管理员 A 的会话: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != conversationA {
		t.Fatalf("管理员 A 的会话列表=%+v，期望只包含 %d", rows, conversationA)
	}
	if _, err := service.GetConversation(ctx, tenantID, conversationB, principal.UserID, "session", 101); !errors.Is(err, ErrNotFound) {
		t.Fatalf("管理员 A 读取管理员 B 会话的错误=%v，期望 ErrNotFound", err)
	}
	if _, err := service.ListMessagesByConversation(ctx, tenantID, conversationB, principal.UserID, "session", 101, 50, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("管理员 A 读取管理员 B 消息的错误=%v，期望 ErrNotFound", err)
	}
	if _, err := queries.AppendMessage(ctx, dbhermes.AppendMessageParams{
		TenantID: tenantID, ConversationID: conversationB, OwnerUserID: principal.UserID,
		ActorSource: "session", ActorID: 101, Role: "user", Content: content,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("管理员 A 向管理员 B 会话追加消息的错误=%v，期望 pgx.ErrNoRows", err)
	}
	updated, err := queries.UpdateConversationLastMessageAt(ctx, dbhermes.UpdateConversationLastMessageAtParams{
		ID: conversationB, TenantID: tenantID, OwnerUserID: principal.UserID,
		ActorSource: "session", ActorID: 101, Ts: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if err != nil || updated != 0 {
		t.Fatalf("管理员 A 更新管理员 B 会话时间结果=(%d,%v)，期望 (0,nil)", updated, err)
	}
	if err := service.SoftDeleteConversationWithAudit(ctx, tenantID, principal.UserID, conversationB, "session", 101, AuditFields{
		ActorSource: "session", ActorID: 101, ActorRole: "tenant_operator",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("管理员 A 删除管理员 B 会话的错误=%v，期望 ErrNotFound", err)
	}
	var deleted bool
	if err := pool.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM hermes_conversations WHERE id=$1`, conversationB).Scan(&deleted); err != nil {
		t.Fatalf("检查管理员 B 会话: %v", err)
	}
	if deleted {
		t.Fatal("管理员 B 的会话被其它管理员删除")
	}
}
