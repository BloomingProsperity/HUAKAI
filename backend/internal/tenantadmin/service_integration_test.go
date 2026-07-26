//go:build integration_pg

package tenantadmin

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	obsdlq "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

func TestServiceCreateTenantAndFirstAdminIsAtomic(t *testing.T) {
	ctx := context.Background()
	pool := openTenantAdminPool(t, ctx)
	platformTenantID := seedTenantAdminPlatform(t, ctx, pool)
	service := NewService(pool, platformTenantID)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")

	result, err := service.Create(ctx, tenantAdminCreateInput(suffix, admin.RolePlatformAdmin))
	if err != nil {
		t.Fatalf("创建租户与首管理员: %v", err)
	}
	t.Cleanup(func() { cleanupTenantAdminTenant(pool, result.Tenant.ID, platformTenantID) })

	if result.Tenant.Status != StatusActive || result.Tenant.Version != 1 || result.FirstAdminID <= 0 {
		t.Fatalf("创建结果=%+v，期望 active/version=1 且首管理员存在", result)
	}
	var role, userStatus, passwordHash string
	var wallet decimal.Decimal
	if err := pool.QueryRow(ctx, `
SELECT u.role, u.status, COALESCE(u.password_hash, ''), w.balance::text
FROM users u
JOIN tenant_wallets w ON w.tenant_id=u.tenant_id
WHERE u.tenant_id=$1 AND u.id=$2`,
		result.Tenant.ID, result.FirstAdminID,
	).Scan(&role, &userStatus, &passwordHash, &wallet); err != nil {
		t.Fatalf("读取首管理员与租户钱包: %v", err)
	}
	if role != "admin" || userStatus != "active" || !strings.HasPrefix(passwordHash, "$argon2id$") || !wallet.IsZero() {
		t.Fatalf("首管理员/钱包 role=%q status=%q hash=%q wallet=%s", role, userStatus, passwordHash, wallet)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int
FROM admin_audit_events
WHERE tenant_id=$1 AND action='create_tenant' AND target_type='tenant' AND target_id=$1`,
		result.Tenant.ID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("读取建租户日志: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("建租户日志 count=%d want 1", auditCount)
	}

	failingName := "tenant-atomic-fail-" + suffix
	failingInput := tenantAdminCreateInput(suffix+"-fail", "invalid_actor_role")
	failingInput.Name = failingName
	if _, err := service.Create(ctx, failingInput); err == nil {
		t.Fatal("日志约束拒绝时创建租户意外成功")
	}
	var leftover int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM tenants WHERE name=$1`, failingName).Scan(&leftover); err != nil {
		t.Fatalf("读取失败事务残留: %v", err)
	}
	if leftover != 0 {
		t.Fatalf("日志失败后残留租户 count=%d want 0", leftover)
	}
}

func TestServiceLifecycleRevokesSessionsAndDeleteRequiresFreshSafeImpact(t *testing.T) {
	ctx := context.Background()
	pool := openTenantAdminPool(t, ctx)
	platformTenantID := seedTenantAdminPlatform(t, ctx, pool)
	service := NewService(pool, platformTenantID)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	created, err := service.Create(ctx, tenantAdminCreateInput(suffix, admin.RolePlatformAdmin))
	if err != nil {
		t.Fatalf("创建生命周期租户: %v", err)
	}
	tenantID, userID := created.Tenant.ID, created.FirstAdminID
	t.Cleanup(func() { cleanupTenantAdminTenant(pool, tenantID, platformTenantID) })

	oldBundle := tenantAdminSessionBundle(tenantID, userID, time.Now().UTC())
	if _, err := usersession.NewPostgresStore(pool).CreateSession(
		ctx, oldBundle, usersession.SessionCreatePolicy{ExpectedAuthVersion: 1}, time.Now().UTC(),
	); err != nil {
		t.Fatalf("创建停用前会话: %v", err)
	}

	disabled, err := service.SetStatus(ctx, StatusInput{
		TenantID: tenantID, Status: StatusDisabled, ExpectedVersion: 1,
		Audit: tenantAdminAudit("停用租户"),
	})
	if err != nil {
		t.Fatalf("停用租户: %v", err)
	}
	if !disabled.Changed || disabled.Tenant.Version != 2 || disabled.SessionsRevoked != 1 {
		t.Fatalf("停用结果=%+v，期望 version=2 且撤销 1 个会话族", disabled)
	}
	assertTenantAdminSessionRevoked(t, ctx, pool, oldBundle)

	rejectedBundle := tenantAdminSessionBundle(tenantID, userID, time.Now().UTC())
	_, err = usersession.NewPostgresStore(pool).CreateSession(
		ctx, rejectedBundle, usersession.SessionCreatePolicy{ExpectedAuthVersion: 1}, time.Now().UTC(),
	)
	if !errors.Is(err, usersession.ErrUserIneligible) {
		t.Fatalf("停用租户创建新会话 err=%v want ErrUserIneligible", err)
	}
	var rejectedRows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*)::int FROM session_families WHERE id=$1::uuid`,
		rejectedBundle.Family.ID,
	).Scan(&rejectedRows); err != nil {
		t.Fatalf("读取被拒会话: %v", err)
	}
	if rejectedRows != 0 {
		t.Fatalf("停用租户仍落下新会话 count=%d want 0", rejectedRows)
	}

	enabled, err := service.SetStatus(ctx, StatusInput{
		TenantID: tenantID, Status: StatusActive, ExpectedVersion: 2,
		Audit: tenantAdminAudit("重新启用租户"),
	})
	if err != nil {
		t.Fatalf("重新启用租户: %v", err)
	}
	if !enabled.Changed || enabled.Tenant.Version != 3 {
		t.Fatalf("启用结果=%+v want changed/version=3", enabled)
	}
	assertTenantAdminSessionRevoked(t, ctx, pool, oldBundle)

	newBundle := tenantAdminSessionBundle(tenantID, userID, time.Now().UTC())
	if _, err := usersession.NewPostgresStore(pool).CreateSession(
		ctx, newBundle, usersession.SessionCreatePolicy{ExpectedAuthVersion: 1}, time.Now().UTC(),
	); err != nil {
		t.Fatalf("启用后创建新会话: %v", err)
	}
	disabledAgain, err := service.SetStatus(ctx, StatusInput{
		TenantID: tenantID, Status: StatusDisabled, ExpectedVersion: 3,
		Audit: tenantAdminAudit("删除前停用"),
	})
	if err != nil {
		t.Fatalf("删除前再次停用: %v", err)
	}
	if disabledAgain.Tenant.Version != 4 || disabledAgain.SessionsRevoked != 1 {
		t.Fatalf("再次停用结果=%+v want version=4/sessions=1", disabledAgain)
	}
	assertTenantAdminSessionRevoked(t, ctx, pool, newBundle)

	if _, err := service.SetStatus(ctx, StatusInput{
		TenantID: platformTenantID, Status: StatusDisabled, ExpectedVersion: 1,
		Audit: tenantAdminAudit("禁止停用平台租户"),
	}); !errors.Is(err, ErrPlatformTenant) {
		t.Fatalf("停用平台工作租户 err=%v want ErrPlatformTenant", err)
	}

	cleanImpact, err := service.InspectDelete(ctx, tenantID)
	if err != nil {
		t.Fatalf("读取删除影响: %v", err)
	}
	if cleanImpact.Blocked || cleanImpact.ImpactHash == "" || cleanImpact.Resources.TenantAdmins != 1 {
		t.Fatalf("干净删除影响=%+v，期望未阻塞且保留首管理员资源计数", cleanImpact)
	}
	rewardEventID := "tenant-delete-signup-reward-" + suffix
	if _, err := pool.Exec(ctx, `
	INSERT INTO outbox_events (id, tenant_id, event_type, priority, payload, status)
	VALUES ($1,$2,$3,'high',
	        jsonb_build_object('tenant_id',$2::bigint,'user_id',$4::bigint,'reward_kind','signup_bonus','amount_cents',100),
	        'pending')`,
		rewardEventID, tenantID, obsdlq.EventTypeSignupReward, userID,
	); err != nil {
		t.Fatalf("制造未完成注册奖励恢复事实: %v", err)
	}
	rewardImpact, err := service.InspectDelete(ctx, tenantID)
	if err != nil {
		t.Fatalf("读取注册奖励阻塞影响: %v", err)
	}
	if !rewardImpact.Blocked || rewardImpact.Blockers.SignupRewardRecoveries != 1 ||
		rewardImpact.ImpactHash == cleanImpact.ImpactHash {
		t.Fatalf("注册奖励删除影响=%+v，期望 blocked/recoveries=1/hash 变化", rewardImpact)
	}
	if _, err := service.Delete(ctx, DeleteInput{
		TenantID: tenantID, ExpectedVersion: 4, ImpactHash: rewardImpact.ImpactHash,
		Audit: tenantAdminAudit("拒绝删除存在注册奖励恢复事实的租户"),
	}); !errors.Is(err, ErrDeleteBlocked) {
		t.Fatalf("注册奖励待恢复时删除 err=%v want ErrDeleteBlocked", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE outbox_events SET status='completed' WHERE id=$1 AND tenant_id=$2`,
		rewardEventID, tenantID,
	); err != nil {
		t.Fatalf("完成注册奖励恢复事实: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO user_balances (tenant_id, user_id, balance, held)
VALUES ($1,$2,1,0)`, tenantID, userID); err != nil {
		t.Fatalf("制造删除前余额变化: %v", err)
	}
	if _, err := service.Delete(ctx, DeleteInput{
		TenantID: tenantID, ExpectedVersion: 4, ImpactHash: cleanImpact.ImpactHash,
		Audit: tenantAdminAudit("使用过期影响快照删除"),
	}); !errors.Is(err, ErrImpactChanged) {
		t.Fatalf("影响变化后删除 err=%v want ErrImpactChanged", err)
	}
	blockedImpact, err := service.InspectDelete(ctx, tenantID)
	if err != nil {
		t.Fatalf("读取有余额的删除影响: %v", err)
	}
	if !blockedImpact.Blocked || blockedImpact.Blockers.UserBalanceRows != 1 {
		t.Fatalf("有余额删除影响=%+v want blocked/user_balance_rows=1", blockedImpact)
	}
	if _, err := service.Delete(ctx, DeleteInput{
		TenantID: tenantID, ExpectedVersion: 4, ImpactHash: blockedImpact.ImpactHash,
		Audit: tenantAdminAudit("拒绝删除有余额租户"),
	}); !errors.Is(err, ErrDeleteBlocked) {
		t.Fatalf("有余额删除 err=%v want ErrDeleteBlocked", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE user_balances SET balance=0 WHERE tenant_id=$1 AND user_id=$2`, tenantID, userID); err != nil {
		t.Fatalf("清零测试余额: %v", err)
	}
	finalImpact, err := service.InspectDelete(ctx, tenantID)
	if err != nil {
		t.Fatalf("读取最终删除影响: %v", err)
	}
	if finalImpact.Blocked || finalImpact.ImpactHash == blockedImpact.ImpactHash {
		t.Fatalf("最终删除影响=%+v，期望解除阻塞且 hash 变化", finalImpact)
	}
	deleted, err := service.Delete(ctx, DeleteInput{
		TenantID: tenantID, ExpectedVersion: 4, ImpactHash: finalImpact.ImpactHash,
		Audit: tenantAdminAudit("确认软删除租户"),
	})
	if err != nil {
		t.Fatalf("软删除租户: %v", err)
	}
	if deleted.Tenant.Status != StatusDeleted || deleted.Tenant.Version != 5 || deleted.Tenant.DeletedAt == nil {
		t.Fatalf("删除结果=%+v want deleted/version=5/deleted_at", deleted)
	}
	var retainedUsers, retainedWallets, deleteLogs int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM users WHERE tenant_id=$1`, tenantID).Scan(&retainedUsers); err != nil {
		t.Fatalf("读取保留用户: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM tenant_wallets WHERE tenant_id=$1`, tenantID).Scan(&retainedWallets); err != nil {
		t.Fatalf("读取保留钱包: %v", err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int FROM admin_audit_events
WHERE tenant_id=$1 AND action='delete_tenant'`, tenantID).Scan(&deleteLogs); err != nil {
		t.Fatalf("读取删除日志: %v", err)
	}
	if retainedUsers != 1 || retainedWallets != 1 || deleteLogs != 1 {
		t.Fatalf("软删除保留 users/wallets/logs=%d/%d/%d want 1/1/1", retainedUsers, retainedWallets, deleteLogs)
	}
}

func openTenantAdminPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("HUAKAI_TEST_DATABASE_URL"))
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("HUAKAI_DATABASE_URL"))
	}
	if dsn == "" {
		t.Skip("未设置 HUAKAI_TEST_DATABASE_URL/HUAKAI_DATABASE_URL，跳过 integration_pg")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("连接 PostgreSQL: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("探测 PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedTenantAdminPlatform(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int64 {
	t.Helper()
	var tenantID int64
	name := "tenant-admin-platform-" + uuid.NewString()
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, name).Scan(&tenantID); err != nil {
		t.Fatalf("创建测试平台租户: %v", err)
	}
	return tenantID
}

func tenantAdminCreateInput(suffix, actorRole string) CreateInput {
	return CreateInput{
		Name:             "tenant-admin-" + suffix,
		AdminEmail:       "tenant-admin-" + suffix + "@example.test",
		AdminDisplayName: "租户管理员",
		AdminPassword:    "StrongPass!2026",
		Audit: AuditInput{
			ActorID: "admin_token:305", ActorRole: actorRole,
			RequestID: "tenant-admin-" + suffix, Reason: "集成测试创建租户",
		},
	}
}

func tenantAdminAudit(reason string) AuditInput {
	return AuditInput{
		ActorID: "admin_token:305", ActorRole: admin.RolePlatformAdmin,
		RequestID: uuid.NewString(), Reason: reason,
	}
}

func tenantAdminSessionBundle(tenantID, userID int64, now time.Time) usersession.SessionBundle {
	familyID := uuid.NewString()
	return usersession.SessionBundle{
		Family: usersession.SessionFamily{
			ID: familyID, TenantID: tenantID, UserID: userID,
			Status: usersession.FamilyStatusActive, Generation: 1, AuthVersion: 1,
			CreatedAt: now, LastActiveAt: now, DeviceInfo: map[string]any{"test": true},
		},
		RefreshToken: usersession.RefreshToken{
			ID: uuid.NewString(), TenantID: tenantID, FamilyID: familyID,
			TokenHash: []byte(uuid.NewString()), Generation: 1,
			Status:    usersession.RefreshTokenStatusActive,
			ExpiresAt: now.Add(time.Hour), CreatedAt: now,
		},
		SessionToken: usersession.SessionToken{
			ID: uuid.NewString(), TenantID: tenantID, FamilyID: familyID,
			TokenHash: []byte(uuid.NewString()), Generation: 1,
			ExpiresAt: now.Add(time.Hour), CreatedAt: now,
		},
	}
}

func assertTenantAdminSessionRevoked(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	bundle usersession.SessionBundle,
) {
	t.Helper()
	var familyStatus, refreshStatus string
	var sessionRevoked bool
	if err := pool.QueryRow(ctx, `
SELECT sf.status, rt.status, st.revoked_at IS NOT NULL
FROM session_families sf
JOIN refresh_tokens rt ON rt.tenant_id=sf.tenant_id AND rt.family_id=sf.id
JOIN session_tokens st ON st.tenant_id=sf.tenant_id AND st.family_id=sf.id
WHERE sf.id=$1::uuid`,
		bundle.Family.ID,
	).Scan(&familyStatus, &refreshStatus, &sessionRevoked); err != nil {
		t.Fatalf("读取会话撤销状态: %v", err)
	}
	if familyStatus != "revoked" || refreshStatus != "revoked" || !sessionRevoked {
		t.Fatalf("会话撤销状态 family/refresh/session=%q/%q/%v", familyStatus, refreshStatus, sessionRevoked)
	}
}

func cleanupTenantAdminTenant(pool *pgxpool.Pool, tenantIDs ...int64) {
	ctx := context.Background()
	for _, tenantID := range tenantIDs {
		if tenantID <= 0 {
			continue
		}
		_, _ = pool.Exec(ctx, `DELETE FROM session_tokens WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM session_families WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM admin_audit_events WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM user_balances WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM tenant_wallets WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenantID)
	}
}
