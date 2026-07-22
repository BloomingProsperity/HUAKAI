//go:build integration_pg

package hermesprincipal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	dbadministrator "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	dbauth "github.com/BloomingProsperity/HUAKAI/internal/db/auth"
)

func openPrincipalTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("HUAKAI_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("未设置 HUAKAI_TEST_DATABASE_URL 或 HUAKAI_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
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

func seedPrincipalTenant(t *testing.T, pool *pgxpool.Pool, status string) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var tenantID int64
	name := fmt.Sprintf("hermes-principal-%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name, status) VALUES ($1, $2) RETURNING id`,
		name, status,
	).Scan(&tenantID); err != nil {
		t.Fatalf("创建测试租户: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM hermes_service_principals WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM api_keys WHERE tenant_id=$1 AND purpose='hermes'`, tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE tenant_id=$1 AND principal_kind='service'`, tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})
	return tenantID
}

func requireSQLState(t *testing.T, err error, state string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("错误类型=%T，期望 PostgreSQL 错误 %s: %v", err, state, err)
	}
	if pgErr.Code != state {
		t.Fatalf("SQLSTATE=%s，期望 %s: %v", pgErr.Code, state, err)
	}
}

func TestStoreEnsureRealPG(t *testing.T) {
	pool := openPrincipalTestPool(t)
	tenantID := seedPrincipalTenant(t, pool, "active")
	store := NewStore(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	const workers = 12
	results := make([]Principal, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index], errs[index] = store.Ensure(ctx, tenantID)
		}(i)
	}
	wg.Wait()

	want := results[0]
	if errs[0] != nil {
		t.Fatalf("首次 Ensure: %v", errs[0])
	}
	if want.TenantID != tenantID || want.UserID <= 0 || want.APIKeyID <= 0 {
		t.Fatalf("服务主体无效: %+v", want)
	}
	for i := 1; i < workers; i++ {
		if errs[i] != nil {
			t.Fatalf("并发 Ensure[%d]: %v", i, errs[i])
		}
		if results[i] != want {
			t.Fatalf("并发 Ensure[%d]=%+v，期望 %+v", i, results[i], want)
		}
	}

	var mappingCount, serviceUserCount, serviceKeyCount int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM hermes_service_principals WHERE tenant_id=$1),
		(SELECT count(*) FROM users WHERE tenant_id=$1 AND principal_kind='service'),
		(SELECT count(*) FROM api_keys WHERE tenant_id=$1 AND purpose='hermes')`,
		tenantID,
	).Scan(&mappingCount, &serviceUserCount, &serviceKeyCount); err != nil {
		t.Fatalf("统计服务主体: %v", err)
	}
	if mappingCount != 1 || serviceUserCount != 1 || serviceKeyCount != 1 {
		t.Fatalf("主体行数=(%d,%d,%d)，期望各为 1", mappingCount, serviceUserCount, serviceKeyCount)
	}

	var kind, role, userStatus, purpose, keyHash, keyPrefix, keyStatus string
	var emailMissing, passwordMissing bool
	if err := pool.QueryRow(ctx, `SELECT u.principal_kind, u.role, u.status,
		u.email IS NULL, u.password_hash IS NULL,
		k.purpose, k.key_hash, k.key_prefix, k.status
		FROM users u
		JOIN api_keys k ON k.tenant_id=u.tenant_id AND k.user_id=u.id
		WHERE u.tenant_id=$1 AND u.id=$2 AND k.id=$3`,
		want.TenantID, want.UserID, want.APIKeyID,
	).Scan(&kind, &role, &userStatus, &emailMissing, &passwordMissing,
		&purpose, &keyHash, &keyPrefix, &keyStatus); err != nil {
		t.Fatalf("读取服务主体性质: %v", err)
	}
	if kind != "service" || role != "user" || userStatus != "active" || !emailMissing || !passwordMissing {
		t.Fatalf("服务用户性质异常: kind=%s role=%s status=%s emailMissing=%v passwordMissing=%v",
			kind, role, userStatus, emailMissing, passwordMissing)
	}
	wantPrefix := "hk_hermes_" + strconv.FormatInt(tenantID, 10)
	if purpose != "hermes" || keyHash != "disabled-internal-service-principal" || keyPrefix != wantPrefix || keyStatus != "active" {
		t.Fatalf("服务 Key 性质异常: purpose=%s hash=%q prefix=%q status=%s",
			purpose, keyHash, keyPrefix, keyStatus)
	}

	authRows, err := dbauth.New(pool).LookupAPIKeysByPrefix(ctx, wantPrefix)
	if err != nil {
		t.Fatalf("公开鉴权查询: %v", err)
	}
	if len(authRows) != 0 {
		t.Fatalf("内部 Key 泄漏到公开鉴权候选: %d 行", len(authRows))
	}
	adminRows, err := dbadministrator.New(pool).AdminListAPIKeysForTenant(ctx,
		dbadministrator.AdminListAPIKeysForTenantParams{TenantID: tenantID, PageLimit: 100})
	if err != nil {
		t.Fatalf("管理员 Key 列表: %v", err)
	}
	if len(adminRows) != 0 {
		t.Fatalf("内部 Key 泄漏到普通管理员列表: %d 行", len(adminRows))
	}

	_, err = pool.Exec(ctx, `UPDATE users SET display_name='不应成功' WHERE id=$1`, want.UserID)
	requireSQLState(t, err, "42501")
	_, err = pool.Exec(ctx, `DELETE FROM api_keys WHERE id=$1`, want.APIKeyID)
	requireSQLState(t, err, "42501")

	if _, err := pool.Exec(ctx, `DELETE FROM hermes_service_principals WHERE tenant_id=$1`, tenantID); err != nil {
		t.Fatalf("删除内部映射: %v", err)
	}
	if tag, err := pool.Exec(ctx, `DELETE FROM api_keys WHERE id=$1`, want.APIKeyID); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("回收内部 Key: affected=%d err=%v", tag.RowsAffected(), err)
	}
	if tag, err := pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, want.UserID); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("回收服务用户: affected=%d err=%v", tag.RowsAffected(), err)
	}
}

func TestStoreEnsureRejectsUnavailableTenantRealPG(t *testing.T) {
	pool := openPrincipalTestPool(t)
	tenantID := seedPrincipalTenant(t, pool, "disabled")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := NewStore(pool).Ensure(ctx, tenantID)
	if !errors.Is(err, ErrTenantMissing) {
		t.Fatalf("Ensure 错误=%v，期望 ErrTenantMissing", err)
	}
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE tenant_id=$1 AND principal_kind='service'`, tenantID,
	).Scan(&count); err != nil {
		t.Fatalf("统计禁用租户服务用户: %v", err)
	}
	if count != 0 {
		t.Fatalf("禁用租户创建了 %d 个服务用户", count)
	}
}
