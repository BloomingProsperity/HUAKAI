//go:build integration_pg

package admin

import (
	"context"
	"testing"
	"time"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// role 制单登录 S3 修复回归(真库):API key 签发限流的分桶跨 P2b-1 审计格式迁移保持连续。
// 老部署写的裸 TokenID 行("55")与新格式行("admin_token:55")必须同桶计数,
// 否则部署瞬间该 admin 的 30/小时 滚动窗被清零(原 S3 缺陷)。
// 变异:把 CountIssuanceInWindow 谓词改回单键 actor_id=$1 → 老行不计 → 首断言 RED。
func TestIssuanceRateWindowSurvivesActorFormatMigration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)

	seed := func(actorID string) {
		t.Helper()
		// tenant_id 留 NULL(deny-audit 同款,避 FK);target_id 非 NULL 才计入限流窗。
		if _, err := pool.Exec(ctx, `
INSERT INTO admin_audit_events (tenant_id, actor_id, actor_role, action, target_type, target_id, occurred_at)
VALUES (NULL, $1, 'platform_admin', 'issue_api_key', 'api_key', 12345, now())`, actorID); err != nil {
			t.Fatalf("种审计行(%s): %v", actorID, err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM admin_audit_events WHERE actor_id IN ('55','admin_token:55') AND target_id=12345`)
	})

	seed("55")             // P2b-1 之前的老格式(裸 TokenID)
	seed("admin_token:55") // P2b-1 之后的新格式(AuditActor())

	q := admindb.New(pool)
	count, err := q.CountIssuanceInWindow(ctx, admindb.CountIssuanceInWindowParams{
		ActorID:       "admin_token:55",
		LegacyActorID: legacyActorKey(AdminIdentity{TokenID: 55, Source: AdminSourceToken}),
		WindowSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("CountIssuanceInWindow: %v", err)
	}
	if count != 2 {
		t.Fatalf("新老格式应同桶计数=2(限流窗跨格式迁移连续),得 %d", count)
	}

	// 反向:别的 token 的老行不得被误计入本桶(legacy 键是精确匹配非通配)。
	seed("56")
	count, err = q.CountIssuanceInWindow(ctx, admindb.CountIssuanceInWindowParams{
		ActorID:       "admin_token:55",
		LegacyActorID: "55",
		WindowSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("CountIssuanceInWindow(二次): %v", err)
	}
	if count != 2 {
		t.Fatalf("他人老行('56')不得入桶,应仍=2,得 %d", count)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM admin_audit_events WHERE actor_id='56' AND target_id=12345`)
	})

	// session 源:无老格式,legacyActorKey 返回同串,OR 无副作用(桶=自己的 admin_user:N)。
	if got := legacyActorKey(AdminIdentity{UserID: 42, Source: AdminSourceSession}); got != "admin_user:42" {
		t.Fatalf("session 源 legacy 键应为同串 admin_user:42,得 %q", got)
	}
}
