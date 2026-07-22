//go:build integration_pg

package tenantcapability

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func openCapabilityPool(t *testing.T) *pgxpool.Pool {
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

func seedCapabilityTenant(t *testing.T, pool *pgxpool.Pool, status string) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var tenantID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name, status) VALUES ($1, $2) RETURNING id`,
		fmt.Sprintf("capability-%d", time.Now().UnixNano()), status,
	).Scan(&tenantID); err != nil {
		t.Fatalf("创建测试租户: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM tenant_admin_capability_grants WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM admin_audit_events WHERE target_type='tenant' AND target_id=$1`, tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})
	return tenantID
}

func TestStoreGrantLifecycleRealPG(t *testing.T) {
	pool := openCapabilityPool(t)
	tenantID := seedCapabilityTenant(t, pool, "active")
	store := NewStore(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	allowed, err := store.Allowed(ctx, tenantID, HermesOperations)
	if err != nil || allowed {
		t.Fatalf("初始 Allowed()=(%v,%v)，期望 (false,nil)", allowed, err)
	}
	initial, err := store.List(ctx, tenantID)
	if err != nil {
		t.Fatalf("初始 List: %v", err)
	}
	if len(initial) != 2 || initial[1].Capability != HermesOperations || initial[1].Configured || initial[1].Enabled {
		t.Fatalf("初始能力投影异常: %+v", initial)
	}

	const workers = 8
	results := make([]SetResult, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index], errs[index] = store.Set(ctx, SetInput{
				TenantID:   tenantID,
				Capability: HermesOperations,
				Enabled:    true,
				Actor:      "admin_token:9001",
				ActorRole:  "platform_admin",
				Reason:     "授权租户管理员使用运维助手",
				RequestID:  fmt.Sprintf("grant-%d", index),
			})
		}(i)
	}
	wg.Wait()
	changed := 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("并发授权[%d]: %v", i, errs[i])
		}
		if results[i].Changed {
			changed++
		}
	}
	if changed != 1 {
		t.Fatalf("并发授权实际变更次数=%d，期望 1", changed)
	}
	allowed, err = store.Allowed(ctx, tenantID, HermesOperations)
	if err != nil || !allowed {
		t.Fatalf("授权后 Allowed()=(%v,%v)，期望 (true,nil)", allowed, err)
	}

	revoked, err := store.Set(ctx, SetInput{
		TenantID:   tenantID,
		Capability: HermesOperations,
		Enabled:    false,
		Actor:      "admin_token:9001",
		ActorRole:  "platform_admin",
		Reason:     "收回运维助手权限",
		RequestID:  "revoke-hermes",
	})
	if err != nil || !revoked.Changed || revoked.Grant.Enabled || revoked.Grant.RevokedAt == nil {
		t.Fatalf("撤权结果=%+v err=%v", revoked, err)
	}
	allowed, err = store.Allowed(ctx, tenantID, HermesOperations)
	if err != nil || allowed {
		t.Fatalf("撤权后 Allowed()=(%v,%v)，期望 (false,nil)", allowed, err)
	}

	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit_events
		WHERE target_type='tenant' AND target_id=$1
		  AND action IN ('grant_tenant_capability', 'revoke_tenant_capability')
		  AND log_category='security'`, tenantID).Scan(&auditCount); err != nil {
		t.Fatalf("统计授权日志: %v", err)
	}
	if auditCount != 2 {
		t.Fatalf("授权日志数量=%d，期望 2", auditCount)
	}
}

func TestStoreRejectsUnavailableTenantRealPG(t *testing.T) {
	pool := openCapabilityPool(t)
	tenantID := seedCapabilityTenant(t, pool, "disabled")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := NewStore(pool).Set(ctx, SetInput{
		TenantID:   tenantID,
		Capability: HermesOperations,
		Enabled:    true,
		Actor:      "admin_token:9001",
		ActorRole:  "platform_admin",
		Reason:     "不应授权已停用租户",
	})
	if !errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("停用租户错误=%v，期望 ErrTenantNotFound", err)
	}
}
