// HUAKAI · iKun
//go:build integration_pg

package adminuserhttp

import (
	"context"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

// 真 DB 证明 SetUserGroupWithAudit 的原子性(修接线审计 #6d:旧实现把「改组 + 审计」拆成两次
// 独立 pool 写,UPDATE 成功而审计失败 → 组已改却无审计的部分写)。
//
// 注入手法:给审计传一个【非法 actor_role】,触 admin_audit_events 的 actor_role CHECK 约束,
// 让事务内的审计 insert 失败 → 整个 pgx.Tx 回滚 → user_group 必须仍是改前值。
//
// 自证 + 变异判别:
//   - 回滚分支断言「失败后 user_group == before」。若有人把 SetUserGroupWithAudit 退回两次
//     独立 pool 写(非事务),UPDATE 会先提交、user_group 变成 premium → 该断言 red。
//   - 成功分支断言「组改成 premium 且恰有 1 条 set_user_group 审计」,与回滚分支(0 改动/0 审计)
//     形成对照,证明两写确实同生同灭。
func TestSetUserGroupWithAudit_AuditFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	pool := openAdminUsersPool(t, ctx)
	f := newAdminUsersFixture(t, ctx, pool)
	userID := f.seedUser("grp-tx", "active", "user", "0.00000000")

	// 改前基线
	var before string
	if err := pool.QueryRow(ctx, `SELECT user_group FROM users WHERE tenant_id=$1 AND id=$2`, f.tenantID, userID).Scan(&before); err != nil {
		t.Fatalf("read baseline user_group: %v", err)
	}

	store := NewPostgresUserGroupAuditStore(pool)
	if store == nil {
		t.Fatal("store nil")
	}

	// (1) 非法 actor_role → 事务内审计 insert 触 CHECK 失败 → 期望整体回滚
	badInput := unlockAuditInput{ActorID: "tok-bad", ActorRole: "definitely_not_a_valid_role"}
	if err := store.SetUserGroupWithAudit(ctx, f.tenantID, userID, "premium", badInput); err == nil {
		t.Fatal("expected error when audit insert violates actor_role CHECK, got nil")
	}
	var afterFail string
	if err := pool.QueryRow(ctx, `SELECT user_group FROM users WHERE tenant_id=$1 AND id=$2`, f.tenantID, userID).Scan(&afterFail); err != nil {
		t.Fatalf("read user_group after failure: %v", err)
	}
	if afterFail != before {
		t.Fatalf("非原子!审计失败后 user_group 被改了: before=%q after=%q(应回滚保持 before)", before, afterFail)
	}
	var auditCnt int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM admin_audit_events WHERE tenant_id=$1 AND target_id=$2 AND action='set_user_group'`,
		f.tenantID, userID).Scan(&auditCnt); err != nil {
		t.Fatalf("count audit after failure: %v", err)
	}
	if auditCnt != 0 {
		t.Fatalf("回滚后不应有审计行,实得 %d", auditCnt)
	}

	// (2) 合法 actor_role → 期望组改成 premium + 恰 1 条审计
	goodInput := unlockAuditInput{ActorID: "tok-ok", ActorRole: admin.RoleTenantOperator}
	if err := store.SetUserGroupWithAudit(ctx, f.tenantID, userID, "premium", goodInput); err != nil {
		t.Fatalf("valid set group failed: %v", err)
	}
	var afterOK string
	if err := pool.QueryRow(ctx, `SELECT user_group FROM users WHERE tenant_id=$1 AND id=$2`, f.tenantID, userID).Scan(&afterOK); err != nil {
		t.Fatalf("read user_group after success: %v", err)
	}
	if afterOK != "premium" {
		t.Fatalf("成功路径 user_group 应为 premium,实得 %q", afterOK)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM admin_audit_events WHERE tenant_id=$1 AND target_id=$2 AND action='set_user_group'`,
		f.tenantID, userID).Scan(&auditCnt); err != nil {
		t.Fatalf("count audit after success: %v", err)
	}
	if auditCnt != 1 {
		t.Fatalf("成功后应有 1 条 set_user_group 审计,实得 %d", auditCnt)
	}
}
