//go:build integration_pg

// AuditActor 真格式落地的集成测试(P2b-1 回归护栏)。
//
// 现有 issuer/token_issuer 集成测试跑真 handler→真库,但它们只断言
// audit payload jsonb 里含 key_prefix,【从不】断言 admin_audit_events.actor_id
// 这一 text 列的真实值 == AuditActor() 的输出(token 源 = "admin_token:<id>")。
// 那个缺口是 mock 单测掩盖不了、也是本回归(balance_credit 归属被 ParseInt 成 0
// 那类跨层 bug)最容易溜过的地方:actor_id 只在 cleanup 的 DELETE 里被用到,
// 而 DELETE 命中 0 行照样"成功",所以即使写库时归属串写错了,既有测试全绿。
//
// 本文件用真 KeyIssuer/KeyRevoker/AdminTokenIssuer→真库→直接 SELECT actor_id
// 断言其精确等于 caller.AuditActor(),覆盖四条写审计的路径:
//   签发 api_key / 吊销 api_key / 签发 admin_token / 吊销 admin_token。
// 变异:把生产码里任一 ActorID 从 req.Caller.AuditActor() 换成别的串(如 ""、
// 硬编码、或错把 TokenID 直接 Itoa),对应断言立即变红。

package admin

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// auditActorRoleFor 从 admin_audit_events 读回指定 (action,target_id) 那行的
// actor_id 与 actor_role 真实列值。用最新一行(按 id desc)以避免同 target
// 多行(如吊销会先有签发行)相互干扰。
func auditActorRoleFor(t *testing.T, ctx context.Context, f *adminFixture, action, targetType string, targetID int64) (actorID, actorRole string) {
	t.Helper()
	if err := f.pool.QueryRow(ctx,
		`SELECT actor_id, actor_role FROM admin_audit_events
		 WHERE action = $1 AND target_type = $2 AND target_id = $3
		 ORDER BY id DESC LIMIT 1`,
		action, targetType, targetID,
	).Scan(&actorID, &actorRole); err != nil {
		t.Fatalf("读 audit actor(action=%s target=%d): %v", action, targetID, err)
	}
	return actorID, actorRole
}

// -----------------------------------------------------------------------------
// Test 1 — 签发 + 吊销 api_key:actor_id 列 == token 源 AuditActor()
// -----------------------------------------------------------------------------

func TestAuditActor_APIKeyIssueRevoke_ActorIDPersisted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	f := newAdminFixture(t, ctx, pool)

	// caller 用真 resolver 解析出的身份,确保 Source=token、TokenID 是真库 id。
	resolver := NewAdminResolver(admindb.New(pool))
	httpReq := httptest.NewRequest("POST", "/admin/v1/api-keys", nil)
	httpReq.Header.Set("Authorization", "Bearer "+f.adminBearer)
	ident, err := resolver.Resolve(ctx, httpReq)
	if err != nil {
		t.Fatalf("resolver.Resolve: %v", err)
	}
	if ident.Source != AdminSourceToken {
		t.Fatalf("解析出的身份应为 token 源,得 %q", ident.Source)
	}
	wantActor := ident.AuditActor() // "admin_token:<真库id>"
	if wantActor != fmt.Sprintf("admin_token:%d", f.adminTokenID) {
		t.Fatalf("AuditActor()=%q,与真库 token id=%d 不一致", wantActor, f.adminTokenID)
	}

	issuer := NewKeyIssuer(pool)
	res, err := issuer.Issue(ctx, IssueRequest{
		Caller:      ident,
		TenantID:    f.tenantID,
		UserID:      f.userID,
		Name:        "actor-issue-" + f.suffix,
		Environment: EnvLive,
		Reason:      "actor-id 落地断言",
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// 断言签发审计行的 actor_id 精确等于 AuditActor()——不是"包含"、不是 payload,
	// 而是那一列。变异 issuer.go:220 的 ActorID 即红。
	gotActor, gotRole := auditActorRoleFor(t, ctx, f, "issue_api_key", "api_key", res.APIKeyID)
	if gotActor != wantActor {
		t.Fatalf("issue_api_key.actor_id=%q,want %q(token 源归属未真落库)", gotActor, wantActor)
	}
	if gotRole != RolePlatformAdmin {
		t.Fatalf("issue_api_key.actor_role=%q,want platform_admin", gotRole)
	}

	// 吊销同一 key,断言吊销审计行的 actor_id 同样落地。变异 revoker.go:108 即红。
	revoker := NewKeyRevoker(pool)
	if _, err := revoker.Revoke(ctx, RevokeRequest{
		Caller:   ident,
		APIKeyID: res.APIKeyID,
		TenantID: f.tenantID,
		Reason:   "actor-id 吊销断言",
	}); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	gotActor2, gotRole2 := auditActorRoleFor(t, ctx, f, "revoke_api_key", "api_key", res.APIKeyID)
	if gotActor2 != wantActor {
		t.Fatalf("revoke_api_key.actor_id=%q,want %q", gotActor2, wantActor)
	}
	if gotRole2 != RolePlatformAdmin {
		t.Fatalf("revoke_api_key.actor_role=%q,want platform_admin", gotRole2)
	}
}

// -----------------------------------------------------------------------------
// Test 2 — 签发 + 吊销 admin_token:actor_id 列 == token 源 AuditActor()
// -----------------------------------------------------------------------------

func TestAuditActor_AdminTokenIssueRevoke_ActorIDPersisted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	f := newAdminFixture(t, ctx, pool)

	caller := AdminIdentity{TokenID: f.adminTokenID, Source: AdminSourceToken, Role: RolePlatformAdmin}
	wantActor := caller.AuditActor() // "admin_token:<真库id>"

	issuer := NewAdminTokenIssuer(pool)
	res, err := issuer.IssueToken(ctx, TokenIssueRequest{
		Caller:    caller,
		Role:      RolePlatformAdmin,
		Note:      "actor-id token 签发",
		RequestID: "req-actor-" + f.suffix,
	})
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM admin_audit_events WHERE target_id = $1 AND target_type = 'admin_token'`, res.TokenID)
		_, _ = pool.Exec(c, `DELETE FROM admin_tokens WHERE id = $1`, res.TokenID)
	})

	// 签发 admin_token 的审计行 actor_id 精确落地。变异 token_issuer.go:205 即红。
	gotActor, gotRole := auditActorRoleFor(t, ctx, f, "issue_admin_token", "admin_token", res.TokenID)
	if gotActor != wantActor {
		t.Fatalf("issue_admin_token.actor_id=%q,want %q", gotActor, wantActor)
	}
	if gotRole != RolePlatformAdmin {
		t.Fatalf("issue_admin_token.actor_role=%q,want platform_admin", gotRole)
	}

	// 吊销刚签的 token,断言吊销审计行 actor_id 落地。变异 token_issuer.go:276 即红。
	if _, err := issuer.RevokeToken(ctx, TokenRevokeRequest{
		Caller:  caller,
		TokenID: res.TokenID,
		Reason:  "actor-id token 吊销",
	}); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	gotActor2, gotRole2 := auditActorRoleFor(t, ctx, f, "revoke_admin_token", "admin_token", res.TokenID)
	if gotActor2 != wantActor {
		t.Fatalf("revoke_admin_token.actor_id=%q,want %q", gotActor2, wantActor)
	}
	if gotRole2 != RolePlatformAdmin {
		t.Fatalf("revoke_admin_token.actor_role=%q,want platform_admin", gotRole2)
	}
}

// -----------------------------------------------------------------------------
// Test 3 — 判别性:同一 actor 连签两把 key,两行 actor_id 都是同一个真串,
// 且计数窗口查询(WHERE actor_id=$1)能命中它们——证明"写入的 actor_id"和
// "限流计数用的 actor_id"是同一个值(§否则限流按 actor 分桶会与实际行错位)。
// -----------------------------------------------------------------------------

func TestAuditActor_RateWindowKeyMatchesPersistedActorID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	f := newAdminFixture(t, ctx, pool)

	caller := AdminIdentity{TokenID: f.adminTokenID, Source: AdminSourceToken, Role: RolePlatformAdmin}
	wantActor := caller.AuditActor()

	issuer := NewKeyIssuer(pool)
	for n := 0; n < 2; n++ {
		if _, err := issuer.Issue(ctx, IssueRequest{
			Caller:      caller,
			TenantID:    f.tenantID,
			UserID:      f.userID,
			Name:        fmt.Sprintf("rate-actor-%d-%s", n, f.suffix),
			Environment: EnvLive,
		}); err != nil {
			t.Fatalf("Issue #%d: %v", n, err)
		}
	}

	// 直接用生产限流查询同名的谓词:统计该 actor_id 名下 issue_api_key 行数。
	// 若生产写库用的 actor_id 与 AuditActor() 不同(如错写 "admin_token:0"),
	// 这条 WHERE actor_id=wantActor 会计到 0,断言变红——这正是限流分桶失配的形态。
	var cnt int
	if err := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM admin_audit_events
		 WHERE actor_id = $1 AND action = 'issue_api_key' AND tenant_id = $2`,
		wantActor, f.tenantID,
	).Scan(&cnt); err != nil {
		t.Fatalf("count by actor_id: %v", err)
	}
	if cnt != 2 {
		t.Fatalf("按 actor_id=%q 计到 %d 行 issue_api_key,want 2(写库归属串与 AuditActor() 失配 → 限流分桶错位)", wantActor, cnt)
	}
}
