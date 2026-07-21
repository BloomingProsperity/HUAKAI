//go:build integration_pg

package adminuserhttp

import (
	"context"
	"testing"
)

// TestCreateUserWithAudit_RollsBackOnAuditFailure 证明「建用户 + 写 create_user 审计」
// 在同一事务内:用一个违反 admin_audit_events.actor_role CHECK 的非法 actor_role 让审计
// 插入必失败,断言用户随之整体回滚、库中不留孤儿。
//
// 变异刀:把 store 的单事务拆回「先独立建用户、再独立写审计」(修复前的形态),该用例会
// 在库里发现残留用户 → 转红。对照分支再用合法 actor_role 建成同一 email,证明上面确是
// 回滚而非撞唯一约束。
func TestCreateUserWithAudit_RollsBackOnAuditFailure(t *testing.T) {
	ctx := context.Background()
	pool := openAdminUsersPool(t, ctx)
	f := newAdminUsersFixture(t, ctx, pool)
	store := NewPostgresUserCreateStore(pool)

	email := "s5-rollback-" + f.suffix + "@x.test"
	in := userCreateInput{
		TenantID:     f.tenantID,
		Email:        email,
		DisplayName:  "rollback probe",
		PasswordHash: "$argon2id$v=19$m=65536,t=3,p=1$c29tZXNhbHQ$c29tZWhhc2g", // 形状合法的占位散列
		Role:         "user",
	}

	// actor_role 非法(不在 CHECK 白名单)→ 事务内审计插入失败,应连建用户一起回滚。
	badAudit := unlockAuditInput{ActorID: "admin_token:1", ActorRole: "bogus_role"}
	if _, err := store.CreateUserWithAudit(ctx, in, badAudit); err == nil {
		t.Fatal("审计 actor_role 非法本应让 CreateUserWithAudit 失败,却返回 nil")
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE tenant_id=$1 AND email=$2 AND deleted_at IS NULL`,
		f.tenantID, email).Scan(&n); err != nil {
		t.Fatalf("统计用户失败: %v", err)
	}
	if n != 0 {
		t.Fatalf("审计失败时用户必须回滚,却发现 %d 行残留(非原子回归)", n)
	}

	// 对照:合法 actor_role 应成功建号,且同 email 可用(证明上面是回滚而非唯一冲突)。
	okAudit := unlockAuditInput{ActorID: "admin_token:1", ActorRole: "tenant_operator"}
	created, err := store.CreateUserWithAudit(ctx, in, okAudit)
	if err != nil {
		t.Fatalf("合法审计应建号成功: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("建号返回 id==0")
	}
	assertLatestAuditAction(t, ctx, pool, f.tenantID, created.ID, "create_user")
}
