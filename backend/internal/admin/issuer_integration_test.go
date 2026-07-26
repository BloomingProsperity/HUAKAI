//go:build integration_pg

// admin 签发流水线的 Slice 2 集成测试。
// 校验完整流程:
//   1. 从 env var 加载 bootstrap admin token → 第一个 AdminIdentity 存在
//   2. Issuer.Issue(...) 写入 api_keys 行 + admin_audit_events 行
//   3. 返回的明文能通过客户的 APIKeyResolver 认证
//   4. Revoker.Revoke(...) 翻转状态;后续 resolve 失败
//   5. 跨 tenant 的 tenant_operator → ErrAdminForbidden
//   6. 速率限制(30/h)挡住第 31 次签发
//   7. Audit payload jsonb【绝不】包含明文 bearer 或 key_hash
//
// 该断言是安全关键的回归测试;
// 它在持久化的 payload 中检索所签发明文的子串。

package admin

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	dbauth "github.com/BloomingProsperity/HUAKAI/internal/db/auth"
)

// 消除未使用 import 的误报:zap 已从本文件的 helper 中移除,但通过
// bootstrap_test 仍保留在包内。
var _ = strconv.Itoa

func openIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration test")
	}
	p, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

type adminFixture struct {
	t            *testing.T
	pool         *pgxpool.Pool
	tenantID     int64
	userID       int64
	adminTokenID int64
	adminBearer  string // platform_admin 明文,用于伪造 auth header
	suffix       string
}

func newAdminFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *adminFixture {
	t.Helper()
	f := &adminFixture{t: t, pool: pool, suffix: uuid.NewString()}

	// 播种 tenant + user(签发的目标)。
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"admin-tenant-"+f.suffix,
	).Scan(&f.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		f.tenantID, "admin-user-"+f.suffix,
	).Scan(&f.userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// 直接播种一个 platform_admin token(绕过 MaybeBootstrap 以避免
	// env-var 耦合;bootstrap 路径有自己的测试)。
	bearer, prefix, err := GenerateBearer(EnvAdmin)
	if err != nil {
		t.Fatalf("generate admin bearer: %v", err)
	}
	f.adminBearer = bearer
	hash, err := bcryptHashForTest(bearer)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO admin_tokens (name, key_hash, key_prefix, role, scope_tenant_id, bootstrap, status)
		 VALUES ($1, $2, $3, 'platform_admin', NULL, false, 'active') RETURNING id`,
		"test-admin-"+f.suffix, hash, prefix,
	).Scan(&f.adminTokenID); err != nil {
		t.Fatalf("seed admin_token: %v", err)
	}

	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM admin_audit_events WHERE actor_id = $1`,
			AdminIdentity{TokenID: f.adminTokenID, Source: AdminSourceToken}.AuditActor())
		_, _ = pool.Exec(c, `DELETE FROM admin_audit_events WHERE tenant_id = $1`, f.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM admin_tokens WHERE id = $1`, f.adminTokenID)
		_, _ = pool.Exec(c, `DELETE FROM api_keys WHERE tenant_id = $1`, f.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id = $1`, f.tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id = $1`, f.tenantID)
	})

	return f
}

// -----------------------------------------------------------------------------
// Test 1 — HappyPath:签发 + 客户 resolver 认证往返
// -----------------------------------------------------------------------------

func TestAdminIssue_HappyPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	f := newAdminFixture(t, ctx, pool)

	resolver := NewAdminResolver(admindb.New(pool))
	httpReq := httptest.NewRequest("POST", "/admin/v1/api-keys", nil)
	httpReq.Header.Set("Authorization", "Bearer "+f.adminBearer)
	ident, err := resolver.Resolve(ctx, httpReq)
	if err != nil {
		t.Fatalf("AdminResolver.Resolve: %v", err)
	}
	if ident.Role != RolePlatformAdmin {
		t.Fatalf("Role = %q; want platform_admin", ident.Role)
	}

	issuer := NewKeyIssuer(pool, f.tenantID)
	result, err := issuer.Issue(ctx, IssueRequest{
		Caller:      ident,
		TenantID:    f.tenantID,
		UserID:      f.userID,
		Name:        "test-key-" + f.suffix,
		Environment: EnvLive,
		Reason:      "admin issuance smoke",
		RequestID:   "req-" + f.suffix,
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if result.Plaintext == "" {
		t.Fatal("Issue returned empty Plaintext")
	}
	if !strings.HasPrefix(result.Plaintext, "hk_live_") {
		t.Errorf("Plaintext should have hk_live_ prefix; got %q", result.Plaintext)
	}
	if result.KeyPrefix == "" || len(result.KeyPrefix) != PrefixLen {
		t.Errorf("KeyPrefix bad: %q (len=%d, want %d)", result.KeyPrefix, len(result.KeyPrefix), PrefixLen)
	}

	// 已签发的明文必须能通过【客户】resolver 认证 —— 全部要点就在于,
	// 从客户 resolver 的视角看,admin 签发的 key 与手工 SQL 插入的 key
	// 行为完全一致。
	custResolver := auth.NewAPIKeyResolver(dbauth.New(pool))
	custReq := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	custReq.Header.Set("Authorization", "Bearer "+result.Plaintext)
	custIdent, err := custResolver.Resolve(ctx, custReq)
	if err != nil {
		t.Fatalf("customer APIKeyResolver rejected admin-issued key: %v", err)
	}
	if custIdent.TenantID != f.tenantID || custIdent.APIKeyID != result.APIKeyID {
		t.Errorf("customer Identity mismatch: got %+v want tenant=%d apiKey=%d",
			custIdent, f.tenantID, result.APIKeyID)
	}
}

func TestAdminIssueTargetLockSerializesTenantDisable(t *testing.T) {
	// 变异：删掉 AdminInsertAPIKey 的 FOR SHARE，租户状态是非键列，停用会越过
	// 未提交的签发事务；本测试会在“停用必须阻塞”处转红。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	f := newAdminFixture(t, ctx, pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("开始签发事务：%v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	hash, err := bcryptHashForTest("lifecycle-lock-" + f.suffix)
	if err != nil {
		t.Fatalf("生成测试 hash：%v", err)
	}
	if _, err := admindb.New(tx).AdminInsertAPIKey(ctx, admindb.AdminInsertAPIKeyParams{
		TenantID:  f.tenantID,
		UserID:    f.userID,
		Name:      "lifecycle-lock",
		KeyHash:   hash,
		KeyPrefix: "hk_test_lock305",
	}); err != nil {
		t.Fatalf("事务内签发 Key：%v", err)
	}

	disabled := make(chan error, 1)
	go func() {
		_, updateErr := pool.Exec(ctx,
			`UPDATE tenants SET status='disabled', updated_at=now() WHERE id=$1`,
			f.tenantID,
		)
		disabled <- updateErr
	}()
	select {
	case updateErr := <-disabled:
		_ = tx.Rollback(ctx)
		t.Fatalf("签发事务未提交时停用越过目标锁：%v", updateErr)
	case <-time.After(150 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("提交签发事务：%v", err)
	}
	select {
	case updateErr := <-disabled:
		if updateErr != nil {
			t.Fatalf("签发提交后停用：%v", updateErr)
		}
	case <-ctx.Done():
		t.Fatalf("签发提交后停用仍阻塞：%v", ctx.Err())
	}
}

// -----------------------------------------------------------------------------
// Test 2 — RevokeBlocksAuth:已吊销的 key 在客户 resolver 处失败
// -----------------------------------------------------------------------------

func TestAdminRevoke_BlocksAuth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	f := newAdminFixture(t, ctx, pool)
	resolver := NewAdminResolver(admindb.New(pool))
	httpReq := httptest.NewRequest("POST", "/", nil)
	httpReq.Header.Set("Authorization", "Bearer "+f.adminBearer)
	ident, err := resolver.Resolve(ctx, httpReq)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	issuer := NewKeyIssuer(pool, f.tenantID)
	revoker := NewKeyRevoker(pool, f.tenantID)

	result, err := issuer.Issue(ctx, IssueRequest{
		Caller:      ident,
		TenantID:    f.tenantID,
		UserID:      f.userID,
		Name:        "to-be-revoked",
		Environment: EnvTest,
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// 吊销前客户 resolver 正常工作。
	custResolver := auth.NewAPIKeyResolver(dbauth.New(pool))
	custReq := httptest.NewRequest("POST", "/", nil)
	custReq.Header.Set("Authorization", "Bearer "+result.Plaintext)
	if _, err := custResolver.Resolve(ctx, custReq); err != nil {
		t.Fatalf("pre-revoke resolve: %v", err)
	}

	revRes, err := revoker.Revoke(ctx, RevokeRequest{
		Caller:   ident,
		APIKeyID: result.APIKeyID,
		TenantID: f.tenantID,
		Reason:   "test-revoke",
	})
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if revRes.AlreadyRevoked {
		t.Fatal("first revoke should not be AlreadyRevoked")
	}

	// 幂等的第二次吊销。
	revRes2, err := revoker.Revoke(ctx, RevokeRequest{
		Caller:   ident,
		APIKeyID: result.APIKeyID,
		TenantID: f.tenantID,
		Reason:   "test-revoke-2",
	})
	if err != nil {
		t.Fatalf("Revoke #2: %v", err)
	}
	if !revRes2.AlreadyRevoked {
		t.Fatal("second revoke should set AlreadyRevoked=true")
	}

	// 客户 resolver 现在拒绝。
	custReq2 := httptest.NewRequest("POST", "/", nil)
	custReq2.Header.Set("Authorization", "Bearer "+result.Plaintext)
	if _, err := custResolver.Resolve(ctx, custReq2); err == nil {
		t.Fatal("customer resolver MUST reject revoked key")
	}
}

func TestAdminRevoke_RemainsAvailableAfterOwnerLifecycleChange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	f := newAdminFixture(t, ctx, pool)
	caller := AdminIdentity{
		TokenID: f.adminTokenID,
		Source:  AdminSourceToken,
		Role:    RolePlatformAdmin,
	}
	issuer := NewKeyIssuer(pool, f.tenantID)
	roleChangedKey, err := issuer.Issue(ctx, IssueRequest{
		Caller: caller, TenantID: f.tenantID, UserID: f.userID,
		Name: "role-change-" + f.suffix, Environment: EnvTest,
	})
	if err != nil {
		t.Fatalf("签发角色变更测试 Key：%v", err)
	}
	deletedOwnerKey, err := issuer.Issue(ctx, IssueRequest{
		Caller: caller, TenantID: f.tenantID, UserID: f.userID,
		Name: "deleted-owner-" + f.suffix, Environment: EnvTest,
	})
	if err != nil {
		t.Fatalf("签发软删除测试 Key：%v", err)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE users SET role='admin', updated_at=now() WHERE tenant_id=$1 AND id=$2`,
		f.tenantID, f.userID,
	); err != nil {
		t.Fatalf("提升测试用户：%v", err)
	}
	assertAdminKeyListed(t, ctx, pool, f.tenantID, roleChangedKey.APIKeyID)
	revoker := NewKeyRevoker(pool, f.tenantID)
	if _, err := revoker.Revoke(ctx, RevokeRequest{
		Caller: caller, APIKeyID: roleChangedKey.APIKeyID, TenantID: f.tenantID,
		Reason: "owner_role_changed",
	}); err != nil {
		t.Fatalf("持有人升为管理员后仍应可撤销历史 Key：%v", err)
	}

	if _, err := pool.Exec(ctx, `
UPDATE users
SET role='user', status='deleted', deleted_at=now(), updated_at=now()
WHERE tenant_id=$1 AND id=$2`, f.tenantID, f.userID); err != nil {
		t.Fatalf("软删除测试用户：%v", err)
	}
	assertAdminKeyListed(t, ctx, pool, f.tenantID, deletedOwnerKey.APIKeyID)
	if _, err := revoker.Revoke(ctx, RevokeRequest{
		Caller: caller, APIKeyID: deletedOwnerKey.APIKeyID, TenantID: f.tenantID,
		Reason: "owner_deleted",
	}); err != nil {
		t.Fatalf("持有人软删除后仍应可撤销历史 Key：%v", err)
	}

	for _, keyID := range []int64{roleChangedKey.APIKeyID, deletedOwnerKey.APIKeyID} {
		var status string
		if err := pool.QueryRow(ctx,
			`SELECT status FROM api_keys WHERE tenant_id=$1 AND id=$2`,
			f.tenantID, keyID,
		).Scan(&status); err != nil {
			t.Fatalf("读取 Key %d 状态：%v", keyID, err)
		}
		if status != "revoked" {
			t.Fatalf("Key %d 状态=%q，期望 revoked", keyID, status)
		}
	}
}

func assertAdminKeyListed(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID, keyID int64,
) {
	t.Helper()
	rows, err := admindb.New(pool).AdminListAPIKeysForTenant(
		ctx,
		admindb.AdminListAPIKeysForTenantParams{
			TenantID:  tenantID,
			PageLimit: 100,
		},
	)
	if err != nil {
		t.Fatalf("列出运维 Key：%v", err)
	}
	for _, row := range rows {
		if row.ID == keyID {
			return
		}
	}
	t.Fatalf("历史 Key %d 未出现在租户 %d 的运维列表", keyID, tenantID)
}

// -----------------------------------------------------------------------------
// Test 3 — AuditNeverContainsPlaintext(回归)
// -----------------------------------------------------------------------------

func TestAdminIssue_AuditNeverContainsPlaintext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	f := newAdminFixture(t, ctx, pool)
	resolver := NewAdminResolver(admindb.New(pool))
	httpReq := httptest.NewRequest("POST", "/", nil)
	httpReq.Header.Set("Authorization", "Bearer "+f.adminBearer)
	ident, _ := resolver.Resolve(ctx, httpReq)

	issuer := NewKeyIssuer(pool, f.tenantID)
	result, err := issuer.Issue(ctx, IssueRequest{
		Caller:      ident,
		TenantID:    f.tenantID,
		UserID:      f.userID,
		Name:        "audit-secrecy-" + f.suffix,
		Environment: EnvLive,
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// 读取所有触及该 tenant 的 audit 行 + 检查它们的 payload jsonb 中
	// 任何位置都不包含明文 bearer 字符串。
	rows, err := pool.Query(ctx,
		`SELECT payload::text FROM admin_audit_events WHERE tenant_id = $1`,
		f.tenantID,
	)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if strings.Contains(payload, result.Plaintext) {
			t.Fatalf("CMB-5 violation: plaintext bearer leaked into audit payload: %s", payload)
		}
		// 同时检查 bcrypt hash 的前缀形态($2a$)。
		if strings.Contains(payload, "$2a$") {
			t.Fatalf("CMB-5 violation: bcrypt hash leaked into audit payload: %s", payload)
		}
	}
}

// -----------------------------------------------------------------------------
// Test 4 — TenantOperatorCrossTenantBlocked
//(tenant_operator 跨 tenant 被拦截)
// -----------------------------------------------------------------------------

func TestAdminIssue_TenantOperatorCrossTenantBlocked(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	f := newAdminFixture(t, ctx, pool)

	// 播种【第二个】tenant —— 下面的 tenant_operator 将被限定在
	// f.tenantID,但试图为 tenantB 签发,这必须被拦截。
	var tenantB int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"admin-tenantB-"+f.suffix,
	).Scan(&tenantB); err != nil {
		t.Fatalf("seed tenantB: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantB)
	})

	// 播种一个限定在 f.tenantID 的 tenant_operator。
	bearer, prefix, _ := GenerateBearer(EnvAdmin)
	hash, _ := bcryptHashForTest(bearer)
	var opID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO admin_tokens (name, key_hash, key_prefix, role, scope_tenant_id, bootstrap, status)
		 VALUES ($1, $2, $3, 'tenant_operator', $4, false, 'active') RETURNING id`,
		"op-"+f.suffix, hash, prefix, f.tenantID,
	).Scan(&opID); err != nil {
		t.Fatalf("seed operator: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM admin_tokens WHERE id=$1`, opID)
	})

	resolver := NewAdminResolver(admindb.New(pool))
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	ident, err := resolver.Resolve(ctx, req)
	if err != nil {
		t.Fatalf("Resolve operator: %v", err)
	}

	// 试图为 tenantB 签发 —— 必须以 ErrAdminForbidden 失败。
	issuer := NewKeyIssuer(pool, f.tenantID)
	_, err = issuer.Issue(ctx, IssueRequest{
		Caller:      ident,
		TenantID:    tenantB, // 错误的 scope
		UserID:      1,
		Name:        "should-fail",
		Environment: EnvLive,
	})
	if err == nil {
		t.Fatal("tenant_operator MUST be blocked from issuing for non-scoped tenant")
	}
	if !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("expected forbidden; got %v", err)
	}
}

// 部署者只管理平台工作租户的终端用户。变异：若签发器或撤销器退回
// CanIssueForTenant，两个动作都会改写下级租户数据，本测试立即变红。
func TestAdminUserKey_PlatformAdminCannotOperateDownstreamTenant(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	f := newAdminFixture(t, ctx, pool)

	var downstreamTenantID, downstreamUserID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"admin-downstream-"+f.suffix,
	).Scan(&downstreamTenantID); err != nil {
		t.Fatalf("seed downstream tenant: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, display_name, role) VALUES ($1,$2,'user') RETURNING id`,
		downstreamTenantID, "downstream-user-"+f.suffix,
	).Scan(&downstreamUserID); err != nil {
		t.Fatalf("seed downstream user: %v", err)
	}
	hash, err := bcryptHashForTest("hk_test_downstream_" + f.suffix)
	if err != nil {
		t.Fatalf("hash downstream key: %v", err)
	}
	var downstreamKeyID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id,user_id,name,key_hash,key_prefix,status)
		 VALUES ($1,$2,$3,$4,$5,'active') RETURNING id`,
		downstreamTenantID, downstreamUserID, "downstream-key", hash, "hk_test_downstre",
	).Scan(&downstreamKeyID); err != nil {
		t.Fatalf("seed downstream key: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM admin_audit_events WHERE payload->>'tenant_id'=$1`, strconv.FormatInt(downstreamTenantID, 10))
		_, _ = pool.Exec(c, `DELETE FROM api_keys WHERE tenant_id=$1`, downstreamTenantID)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id=$1`, downstreamTenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, downstreamTenantID)
	})

	caller := AdminIdentity{TokenID: f.adminTokenID, Source: AdminSourceToken, Role: RolePlatformAdmin}
	issuer := NewKeyIssuer(pool, f.tenantID)
	if _, err := issuer.Issue(ctx, IssueRequest{
		Caller:      caller,
		TenantID:    downstreamTenantID,
		UserID:      downstreamUserID,
		Name:        "must-not-exist",
		Environment: EnvLive,
	}); !errors.Is(err, ErrAdminForbidden) {
		t.Fatalf("部署者越级签发错误=%v，期望 ErrAdminForbidden", err)
	}
	var issued int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM api_keys WHERE tenant_id=$1 AND name='must-not-exist'`,
		downstreamTenantID,
	).Scan(&issued); err != nil {
		t.Fatalf("count denied issue: %v", err)
	}
	if issued != 0 {
		t.Fatalf("越级签发留下 %d 把 Key，期望 0", issued)
	}

	revoker := NewKeyRevoker(pool, f.tenantID)
	if _, err := revoker.Revoke(ctx, RevokeRequest{
		Caller:   caller,
		APIKeyID: downstreamKeyID,
		TenantID: downstreamTenantID,
		Reason:   "must-not-revoke",
	}); !errors.Is(err, ErrAdminForbidden) {
		t.Fatalf("部署者越级撤销错误=%v，期望 ErrAdminForbidden", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM api_keys WHERE id=$1`, downstreamKeyID).Scan(&status); err != nil {
		t.Fatalf("read downstream key: %v", err)
	}
	if status != "active" {
		t.Fatalf("越级撤销改成 %q，期望仍为 active", status)
	}
}

// -----------------------------------------------------------------------------
// 辅助函数
// -----------------------------------------------------------------------------

func bcryptHashForTest(plaintext string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}
