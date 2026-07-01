//go:build integration_pg

// AdminTokenIssuer 的集成测试。验证:
//   1. platform_admin 能签发 admin token,明文能过 resolver
//   2. 临时 token 到期后被 resolver 拒绝(真过 ExpiresAt 检查)
//   3. 吊销后被 resolver 拒绝
//   4. 只 platform_admin 能签发 / 列举 / 吊销;tenant_operator -> ErrAdminForbidden
//   5. 明文 bearer 绝不入库:DB 的 key_hash 不等于明文,
//      admin_audit_events.payload 不含明文子串
//   6. 列举只返元数据,绝不返 hash
//
// 这些是安全关键回归测试:在持久化的 payload 中检索明文子串、并真正
// 把过期/吊销态喂给生产 resolver。

package admin

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// seedTenantOperatorToken 直接播种一个 tenant_operator admin token,返回其
// AdminIdentity(用于验证非 platform_admin 被拒)。复用 fixture 的 tenant。
func seedTenantOperatorToken(t *testing.T, ctx context.Context, f *adminFixture) AdminIdentity {
	t.Helper()
	bearer, prefix, err := GenerateBearer(EnvAdmin)
	if err != nil {
		t.Fatalf("generate operator bearer: %v", err)
	}
	hash, err := bcryptHashForTest(bearer)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	var tokenID int64
	if err := f.pool.QueryRow(ctx,
		`INSERT INTO admin_tokens (name, key_hash, key_prefix, role, scope_tenant_id, bootstrap, status)
		 VALUES ($1, $2, $3, 'tenant_operator', $4, false, 'active') RETURNING id`,
		"op-token-"+f.suffix, hash, prefix, f.tenantID,
	).Scan(&tokenID); err != nil {
		t.Fatalf("seed operator token: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = f.pool.Exec(c, `DELETE FROM admin_audit_events WHERE actor_id = $1`, AdminIdentity{TokenID: tokenID, Source: AdminSourceToken}.AuditActor())
		_, _ = f.pool.Exec(c, `DELETE FROM admin_tokens WHERE id = $1`, tokenID)
	})
	return AdminIdentity{TokenID: tokenID, Role: RoleTenantOperator, ScopeTenantID: f.tenantID}
}

// -----------------------------------------------------------------------------
// Test 1 — HappyPath:platform_admin 签发,明文过 resolver,且明文不入库
// -----------------------------------------------------------------------------

func TestAdminTokenIssue_HappyPath_PlaintextNotPersisted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	f := newAdminFixture(t, ctx, pool)

	caller := AdminIdentity{TokenID: f.adminTokenID, Role: RolePlatformAdmin}
	issuer := NewAdminTokenIssuer(pool)
	res, err := issuer.IssueToken(ctx, TokenIssueRequest{
		Caller:    caller,
		Role:      RolePlatformAdmin,
		Note:      "smoke",
		RequestID: "req-tok-" + f.suffix,
	})
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	defer func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM admin_audit_events WHERE target_id = $1 AND target_type = 'admin_token'`, res.TokenID)
		_, _ = pool.Exec(c, `DELETE FROM admin_tokens WHERE id = $1`, res.TokenID)
	}()

	if res.Plaintext == "" || !strings.HasPrefix(res.Plaintext, "hk_admin_") {
		t.Fatalf("Plaintext bad: %q", res.Plaintext)
	}

	// 明文能通过 admin resolver 认证。
	resolver := NewAdminResolver(admindb.New(pool))
	httpReq := httptest.NewRequest("POST", "/", nil)
	httpReq.Header.Set("Authorization", "Bearer "+res.Plaintext)
	ident, err := resolver.Resolve(ctx, httpReq)
	if err != nil {
		t.Fatalf("resolver rejected freshly-minted token: %v", err)
	}
	if ident.Role != RolePlatformAdmin || ident.TokenID != res.TokenID {
		t.Errorf("identity mismatch: got %+v", ident)
	}

	// 安全断言:DB 里存的 key_hash 绝不等于明文。
	var storedHash string
	if err := pool.QueryRow(ctx, `SELECT key_hash FROM admin_tokens WHERE id = $1`, res.TokenID).Scan(&storedHash); err != nil {
		t.Fatalf("read stored hash: %v", err)
	}
	if storedHash == res.Plaintext {
		t.Fatal("明文被原样存进了 key_hash —— 严重泄露")
	}
	if strings.Contains(storedHash, res.Plaintext) {
		t.Fatal("key_hash 含明文子串 —— 泄露")
	}

	// 安全断言:audit payload 绝不含明文 bearer 子串。
	var payloadConcat string
	if err := pool.QueryRow(ctx,
		`SELECT coalesce(string_agg(payload::text, ' '), '') FROM admin_audit_events
		 WHERE target_id = $1 AND target_type = 'admin_token'`, res.TokenID,
	).Scan(&payloadConcat); err != nil {
		t.Fatalf("read audit payload: %v", err)
	}
	if strings.Contains(payloadConcat, res.Plaintext) {
		t.Fatalf("audit payload 含明文 bearer —— 泄露: %q", payloadConcat)
	}
	// 但 prefix 应当出现在 audit(取证用),证明 audit 真写了且区分。
	if !strings.Contains(payloadConcat, res.KeyPrefix) {
		t.Fatalf("audit payload 未含 key_prefix,审计未生效: %q", payloadConcat)
	}
}

// -----------------------------------------------------------------------------
// Test 2 — 临时 token 到期后被 resolver 拒绝(真过 ExpiresAt 检查)
// -----------------------------------------------------------------------------

func TestAdminTokenIssue_ExpiredTokenRejectedByResolver(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	f := newAdminFixture(t, ctx, pool)

	caller := AdminIdentity{TokenID: f.adminTokenID, Role: RolePlatformAdmin}
	issuer := NewAdminTokenIssuer(pool)

	// 先签一个未来 token,再把 expires_at 直接改成过去(模拟时间流逝),
	// 这样无需真实等待即可让 resolver 的 ExpiresAt 检查触发。
	future := time.Now().Add(1 * time.Hour)
	res, err := issuer.IssueToken(ctx, TokenIssueRequest{
		Caller:    caller,
		Role:      RolePlatformAdmin,
		ExpiresAt: &future,
		Note:      "temp",
	})
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	defer func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM admin_audit_events WHERE target_id = $1 AND target_type = 'admin_token'`, res.TokenID)
		_, _ = pool.Exec(c, `DELETE FROM admin_tokens WHERE id = $1`, res.TokenID)
	}()

	resolver := NewAdminResolver(admindb.New(pool))
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("Authorization", "Bearer "+res.Plaintext)

	// 到期前:能认证(自证基线,区分性)。
	if _, err := resolver.Resolve(ctx, req); err != nil {
		t.Fatalf("未到期 token 不应被拒: %v", err)
	}

	// 把 expires_at 推到过去。
	if _, err := pool.Exec(ctx,
		`UPDATE admin_tokens SET expires_at = NOW() - interval '1 minute' WHERE id = $1`, res.TokenID,
	); err != nil {
		t.Fatalf("backdate expiry: %v", err)
	}

	// 到期后:必须被拒(ErrAdminUnauthorized)。
	if _, err := resolver.Resolve(ctx, req); err == nil {
		t.Fatal("到期 token 仍能认证 —— 过期检查失效(去掉 operator_auth.go 的 ExpiresAt 检查会让本断言变红)")
	}
}

// -----------------------------------------------------------------------------
// Test 3 — 吊销后被 resolver 拒绝
// -----------------------------------------------------------------------------

func TestAdminTokenRevoke_BlocksResolve(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	f := newAdminFixture(t, ctx, pool)

	caller := AdminIdentity{TokenID: f.adminTokenID, Role: RolePlatformAdmin}
	issuer := NewAdminTokenIssuer(pool)
	res, err := issuer.IssueToken(ctx, TokenIssueRequest{Caller: caller, Role: RolePlatformAdmin})
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	defer func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM admin_audit_events WHERE target_id = $1 AND target_type = 'admin_token'`, res.TokenID)
		_, _ = pool.Exec(c, `DELETE FROM admin_tokens WHERE id = $1`, res.TokenID)
	}()

	resolver := NewAdminResolver(admindb.New(pool))
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("Authorization", "Bearer "+res.Plaintext)
	if _, err := resolver.Resolve(ctx, req); err != nil {
		t.Fatalf("吊销前应能认证: %v", err)
	}

	rev, err := issuer.RevokeToken(ctx, TokenRevokeRequest{Caller: caller, TokenID: res.TokenID, Reason: "rotate"})
	if err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	if rev.AlreadyRevoked {
		t.Fatal("首次吊销不应是 AlreadyRevoked")
	}

	// 吊销后:resolver 必须拒绝(查询 WHERE status='active' 把它过滤掉)。
	if _, err := resolver.Resolve(ctx, req); err == nil {
		t.Fatal("已吊销 token 仍能认证 —— 吊销失效")
	}

	// 幂等:再次吊销返回 AlreadyRevoked。
	rev2, err := issuer.RevokeToken(ctx, TokenRevokeRequest{Caller: caller, TokenID: res.TokenID})
	if err != nil {
		t.Fatalf("二次吊销: %v", err)
	}
	if !rev2.AlreadyRevoked {
		t.Fatal("二次吊销应为 AlreadyRevoked(幂等)")
	}
}

// -----------------------------------------------------------------------------
// Test 4 — 只 platform_admin 能签发 / 列举 / 吊销
// -----------------------------------------------------------------------------

func TestAdminTokenIssue_TenantOperatorForbidden(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	f := newAdminFixture(t, ctx, pool)

	op := seedTenantOperatorToken(t, ctx, f)
	issuer := NewAdminTokenIssuer(pool)

	// 签发:tenant_operator 必须被拒。
	if _, err := issuer.IssueToken(ctx, TokenIssueRequest{Caller: op, Role: RolePlatformAdmin}); err == nil {
		t.Fatal("tenant_operator 竟能签发 admin token —— 越权(去掉 requirePlatformAdmin 守卫会让本断言变红)")
	} else if !isForbidden(err) {
		t.Fatalf("应为 ErrAdminForbidden,得: %v", err)
	}

	// 列举:tenant_operator 必须被拒。
	if _, err := issuer.ListTokens(ctx, op, 50, 0); err == nil {
		t.Fatal("tenant_operator 竟能列举 admin token —— 越权")
	} else if !isForbidden(err) {
		t.Fatalf("ListTokens 应为 ErrAdminForbidden,得: %v", err)
	}

	// 吊销:tenant_operator 必须被拒。
	if _, err := issuer.RevokeToken(ctx, TokenRevokeRequest{Caller: op, TokenID: f.adminTokenID}); err == nil {
		t.Fatal("tenant_operator 竟能吊销 admin token —— 越权")
	} else if !isForbidden(err) {
		t.Fatalf("RevokeToken 应为 ErrAdminForbidden,得: %v", err)
	}

	// 对照:platform_admin 同样的列举调用必须成功(区分性,证明拒绝来自
	// 角色而非别的失败)。
	pa := AdminIdentity{TokenID: f.adminTokenID, Role: RolePlatformAdmin}
	if _, err := issuer.ListTokens(ctx, pa, 50, 0); err != nil {
		t.Fatalf("platform_admin 列举应成功: %v", err)
	}
}

// -----------------------------------------------------------------------------
// Test 5 — 列举不漏 hash,且能看到刚签的 token
// -----------------------------------------------------------------------------

func TestAdminTokenList_NoHashLeak(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	f := newAdminFixture(t, ctx, pool)

	caller := AdminIdentity{TokenID: f.adminTokenID, Role: RolePlatformAdmin}
	issuer := NewAdminTokenIssuer(pool)
	res, err := issuer.IssueToken(ctx, TokenIssueRequest{Caller: caller, Role: RolePlatformAdmin, Name: "listme-" + f.suffix})
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	defer func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM admin_audit_events WHERE target_id = $1 AND target_type = 'admin_token'`, res.TokenID)
		_, _ = pool.Exec(c, `DELETE FROM admin_tokens WHERE id = $1`, res.TokenID)
	}()

	items, err := issuer.ListTokens(ctx, caller, 500, 0)
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	var found *TokenListItem
	for i := range items {
		if items[i].ID == res.TokenID {
			found = &items[i]
			break
		}
	}
	if found == nil {
		t.Fatal("刚签的 token 未出现在列表中")
	}
	if found.KeyPrefix != res.KeyPrefix {
		t.Errorf("KeyPrefix 不一致: list=%q issue=%q", found.KeyPrefix, res.KeyPrefix)
	}
	// TokenListItem 结构上没有 hash/plaintext 字段;此处再核 prefix 不等于
	// 任何完整明文(prefix 是明文截断,理应 != 完整明文)。
	if found.KeyPrefix == res.Plaintext {
		t.Fatal("列表 key_prefix 等于完整明文 —— 泄露")
	}
}

// isForbidden 判定 err 链是否为 ErrAdminForbidden。
func isForbidden(err error) bool {
	return errors.Is(err, ErrAdminForbidden)
}
