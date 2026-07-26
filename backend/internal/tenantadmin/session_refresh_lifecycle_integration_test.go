//go:build integration_pg

package tenantadmin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSessionRefreshSerializesWithTenantDisable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openTenantAdminPool(t, ctx)
	platformTenantID := seedTenantAdminPlatform(t, ctx, pool)
	t.Cleanup(func() { cleanupTenantAdminTenant(pool, platformTenantID) })

	tenantID, userID := seedRefreshLifecycleUser(t, ctx, pool)
	t.Cleanup(func() { cleanupRefreshLifecycleTenant(pool, tenantID) })
	sessionService := refreshLifecycleSessionService(pool)
	issued, err := sessionService.Create(ctx, usersession.CreateInput{
		TenantID: tenantID, UserID: userID, AuthVersion: 1,
		IP: "198.51.100.10", UserAgent: "refresh-lifecycle",
	})
	if err != nil {
		t.Fatalf("创建初始会话: %v", err)
	}

	blocker, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("开始刷新令牌阻塞事务: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err := blocker.Exec(ctx, `
SELECT id
FROM refresh_tokens
WHERE tenant_id=$1 AND token_hash=$2
FOR UPDATE`, tenantID, usersession.HashRefreshToken(issued.RefreshToken)); err != nil {
		t.Fatalf("锁定旧刷新令牌: %v", err)
	}

	refreshed := make(chan refreshLifecycleResult, 1)
	go func() {
		result, refreshErr := sessionService.Refresh(ctx, usersession.RefreshInput{
			TenantID: tenantID, UserID: userID, RefreshToken: issued.RefreshToken,
			IP: "198.51.100.10", UserAgent: "refresh-lifecycle",
		})
		refreshed <- refreshLifecycleResult{issued: result, err: refreshErr}
	}()
	waitForRefreshTokenLock(t, ctx, pool)

	disabled := make(chan error, 1)
	go func() {
		_, disableErr := NewService(pool, platformTenantID).SetStatus(ctx, StatusInput{
			TenantID: tenantID, Status: StatusDisabled, ExpectedVersion: 1,
			Audit: tenantAdminAudit("并发停用刷新中的租户"),
		})
		disabled <- disableErr
	}()
	select {
	case disableErr := <-disabled:
		_ = blocker.Rollback(ctx)
		t.Fatalf("刷新事务未结束时租户停用越过生命周期锁: %v", disableErr)
	case <-time.After(200 * time.Millisecond):
	}

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("释放刷新令牌阻塞: %v", err)
	}
	var refreshResult refreshLifecycleResult
	select {
	case refreshResult = <-refreshed:
		if refreshResult.err != nil {
			t.Fatalf("先取得生命周期锁的刷新失败: %v", refreshResult.err)
		}
	case <-ctx.Done():
		t.Fatalf("刷新未完成: %v", ctx.Err())
	}
	select {
	case disableErr := <-disabled:
		if disableErr != nil {
			t.Fatalf("刷新提交后停用租户: %v", disableErr)
		}
	case <-ctx.Done():
		t.Fatalf("停用未完成: %v", ctx.Err())
	}

	assertDisabledRefreshFacts(t, ctx, pool, tenantID, issued.Family.ID)
	if _, err := sessionService.Validate(
		ctx, refreshResult.issued.SessionToken, "198.51.100.10", "refresh-lifecycle",
	); !errors.Is(err, usersession.ErrFamilyRevoked) {
		t.Fatalf("停用后刷新会话校验错误=%v，期望 ErrFamilyRevoked", err)
	}
}

func TestSessionRefreshRollsBackWhenSessionTokenInsertFails(t *testing.T) {
	ctx := context.Background()
	pool := openTenantAdminPool(t, ctx)
	tenantID, userID := seedRefreshLifecycleUser(t, ctx, pool)
	t.Cleanup(func() { cleanupRefreshLifecycleTenant(pool, tenantID) })
	sessionService := refreshLifecycleSessionService(pool)
	issued, err := sessionService.Create(ctx, usersession.CreateInput{
		TenantID: tenantID, UserID: userID, AuthVersion: 1,
		IP: "198.51.100.20", UserAgent: "refresh-rollback",
	})
	if err != nil {
		t.Fatalf("创建初始会话: %v", err)
	}

	functionName := fmt.Sprintf("huakai_test_fail_refresh_session_%d", tenantID)
	triggerName := functionName + "_trigger"
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.tenant_id = %d AND NEW.generation > 1 THEN
        RAISE EXCEPTION 'injected refresh session insert failure';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER %s
BEFORE INSERT ON session_tokens
FOR EACH ROW EXECUTE FUNCTION %s()`, functionName, tenantID, triggerName, functionName)); err != nil {
		t.Fatalf("安装会话插入失败注入: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON session_tokens; DROP FUNCTION IF EXISTS %s()",
			triggerName, functionName,
		))
	})

	if _, err := sessionService.Refresh(ctx, usersession.RefreshInput{
		TenantID: tenantID, UserID: userID, RefreshToken: issued.RefreshToken,
		IP: "198.51.100.20", UserAgent: "refresh-rollback",
	}); err == nil || !strings.Contains(err.Error(), "injected refresh session insert failure") {
		t.Fatalf("刷新错误=%v，期望会话插入失败", err)
	}

	var generation, activeRefresh, sessionCount int
	if err := pool.QueryRow(ctx, `
SELECT generation
FROM session_families
WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, issued.Family.ID).Scan(&generation); err != nil {
		t.Fatalf("读取失败后的会话代际: %v", err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int
FROM refresh_tokens
WHERE tenant_id=$1 AND family_id=$2::uuid AND status='active'`,
		tenantID, issued.Family.ID,
	).Scan(&activeRefresh); err != nil {
		t.Fatalf("读取失败后的活跃刷新令牌: %v", err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int
FROM session_tokens
WHERE tenant_id=$1 AND family_id=$2::uuid`,
		tenantID, issued.Family.ID,
	).Scan(&sessionCount); err != nil {
		t.Fatalf("读取失败后的访问会话: %v", err)
	}
	if generation != 1 || activeRefresh != 1 || sessionCount != 1 {
		t.Fatalf("失败后代际/活跃刷新/访问会话=%d/%d/%d，期望 1/1/1",
			generation, activeRefresh, sessionCount)
	}
}

type refreshLifecycleResult struct {
	issued usersession.IssuedTokens
	err    error
}

func refreshLifecycleSessionService(pool *pgxpool.Pool) *usersession.Service {
	service := usersession.NewService(usersession.NewPostgresStore(pool))
	service.SigningKey = []byte("0123456789abcdef0123456789abcdef")
	return service
}

func seedRefreshLifecycleUser(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) (int64, int64) {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	var tenantID, userID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"refresh-lifecycle-"+suffix,
	).Scan(&tenantID); err != nil {
		t.Fatalf("创建刷新生命周期租户: %v", err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO users (tenant_id, email, display_name, status, password_version)
VALUES ($1,$2,$3,'active',1)
RETURNING id`, tenantID, "refresh-"+suffix+"@example.test", "刷新生命周期用户",
	).Scan(&userID); err != nil {
		t.Fatalf("创建刷新生命周期用户: %v", err)
	}
	return tenantID, userID
}

func waitForRefreshTokenLock(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var blocked bool
		err := pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM pg_stat_activity
    WHERE datname=current_database()
      AND pid <> pg_backend_pid()
      AND wait_event_type='Lock'
      AND query LIKE '%UPDATE refresh_tokens%'
      AND query LIKE '%status = ''consumed''%'
)`).Scan(&blocked)
		if err != nil {
			t.Fatalf("观察刷新锁等待: %v", err)
		}
		if blocked {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("刷新没有进入预期的令牌行锁等待")
}

func assertDisabledRefreshFacts(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID int64,
	familyID string,
) {
	t.Helper()
	var tenantStatus, familyStatus string
	var activeRefresh, liveSessions int
	if err := pool.QueryRow(ctx, `SELECT status FROM tenants WHERE id=$1`, tenantID).Scan(&tenantStatus); err != nil {
		t.Fatalf("读取停用后租户: %v", err)
	}
	if err := pool.QueryRow(ctx, `
SELECT status
FROM session_families
WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, familyID).Scan(&familyStatus); err != nil {
		t.Fatalf("读取停用后会话族: %v", err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int
FROM refresh_tokens
WHERE tenant_id=$1 AND family_id=$2::uuid AND status='active'`,
		tenantID, familyID,
	).Scan(&activeRefresh); err != nil {
		t.Fatalf("读取停用后刷新令牌: %v", err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int
FROM session_tokens
WHERE tenant_id=$1 AND family_id=$2::uuid AND revoked_at IS NULL`,
		tenantID, familyID,
	).Scan(&liveSessions); err != nil {
		t.Fatalf("读取停用后访问会话: %v", err)
	}
	if tenantStatus != StatusDisabled || familyStatus != string(usersession.FamilyStatusRevoked) ||
		activeRefresh != 0 || liveSessions != 0 {
		t.Fatalf("停用后租户/会话族/活跃刷新/可用会话=%s/%s/%d/%d",
			tenantStatus, familyStatus, activeRefresh, liveSessions)
	}
}

func cleanupRefreshLifecycleTenant(pool *pgxpool.Pool, tenantID int64) {
	if pool == nil || tenantID <= 0 {
		return
	}
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM admin_audit_events WHERE tenant_id=$1`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM session_tokens WHERE tenant_id=$1`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE tenant_id=$1`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM session_families WHERE tenant_id=$1`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE tenant_id=$1`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenantID)
}
