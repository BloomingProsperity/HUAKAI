//go:build integration_pg

// 新设备确认流的真 PostgreSQL 集成测试。校验 (覆盖任务要求的 4 个场景):
//   - confirm 创建 pending + 撤最老腾位 (主路径, 端到端真 PG)
//   - 过期 token 拒绝 (不撤 family)
//   - 重放 (二次 confirm) 幂等不重复撤
//   - 休眠 (MaxActiveFamilies=0) 零副作用 (照常签发, 不落 pending, 不撤 family)
//
// 直接驱动 usersession.Service over PostgresStore (NewPostgresStore), token_hash only 持久化语义
// 由真表 device_confirmations 验证。库连法同 internal/auth/api_key_resolver_integration_test.go:
//   HUAKAI_DATABASE_URL=postgres://huakai:huakai@localhost:5432/<已迁移库>?sslmode=disable

package usersession

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func openDCIntegrationPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
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

func TestCreateSessionDeviceLimitAcrossConcurrentServicesPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openDCIntegrationPool(t, ctx)
	now := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	tenantID, userID := seedDCTenantUser(t, ctx, pool)

	const workers = 24
	const limit = 3
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			svc := NewService(NewPostgresStore(pool))
			svc.SigningKey = testSigningKey()
			svc.Now = func() time.Time { return now }
			svc.MaxActiveFamilies = limit
			svc.DevicePolicy = "deny"
			<-start
			_, err := svc.Create(ctx, CreateInput{
				TenantID: tenantID, UserID: userID,
				IP: "198.51.100.10", UserAgent: "concurrent-client",
			})
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)

	succeeded := 0
	denied := 0
	for err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrDeviceLimitExceeded):
			denied++
		default:
			t.Fatalf("并发创建返回意外错误: %v", err)
		}
	}
	if succeeded != limit || denied != workers-limit {
		t.Fatalf("成功=%d 拒绝=%d，期望成功=%d 拒绝=%d", succeeded, denied, limit, workers-limit)
	}
	if got := countActiveFamiliesPG(t, ctx, pool, tenantID, userID); got != limit {
		t.Fatalf("活跃会话族=%d，期望严格等于 %d", got, limit)
	}
	for table := range map[string]struct{}{"refresh_tokens": {}, "session_tokens": {}} {
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE tenant_id=$1", tenantID).Scan(&count); err != nil {
			t.Fatalf("统计 %s: %v", table, err)
		}
		if count != limit {
			t.Fatalf("%s 行数=%d，期望 %d；拒绝路径不得留下半成品", table, count, limit)
		}
	}
}

// seedDCTenantUser 建一个租户 + 用户, 返回 (tenantID, userID); 登记 cleanup。
func seedDCTenantUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (int64, int64) {
	t.Helper()
	var tenantID, userID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`,
		"dc-tenant-"+time.Now().Format("150405.000000000")).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		tenantID, "dc-user").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM device_confirmations WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM session_tokens WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM refresh_tokens WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM session_families WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM users WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})
	return tenantID, userID
}

// seedDCFamilies 直接插 n 个活跃 family (last_active 递增, 第 0 个最老), 返回最老 family 的 id。
func seedDCFamilies(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID int64, n int, base time.Time) string {
	t.Helper()
	var oldest string
	for i := 0; i < n; i++ {
		var id string
		if err := pool.QueryRow(ctx, `
INSERT INTO session_families (id, tenant_id, user_id, status, generation, created_at, last_active_at)
VALUES (gen_random_uuid(), $1, $2, 'active', 1, $3, $4) RETURNING id::text`,
			tenantID, userID, base.Add(-time.Hour), base.Add(time.Duration(i)*time.Minute),
		).Scan(&id); err != nil {
			t.Fatalf("seed family %d: %v", i, err)
		}
		if i == 0 {
			oldest = id
		}
	}
	return oldest
}

func countActiveFamiliesPG(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, userID int64) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM session_families WHERE tenant_id=$1 AND user_id=$2 AND status='active'`,
		tenantID, userID).Scan(&n); err != nil {
		t.Fatalf("count active families: %v", err)
	}
	return n
}

func familyStatusPG(t *testing.T, ctx context.Context, pool *pgxpool.Pool, familyID string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(ctx, `SELECT status FROM session_families WHERE id=$1::uuid`, familyID).Scan(&s); err != nil {
		t.Fatalf("read family status: %v", err)
	}
	return s
}

func newDCService(pool *pgxpool.Pool, now time.Time) *Service {
	svc := NewService(NewPostgresStore(pool))
	svc.SigningKey = testSigningKey()
	svc.Now = func() time.Time { return now }
	svc.MaxActiveFamilies = 2
	svc.DevicePolicy = "confirm"
	return svc
}

// TestDeviceConfirmation_CreatePendingThenConfirmFreesOldest_PG: 主路径端到端真 PG。
func TestDeviceConfirmation_CreatePendingThenConfirmFreesOldest_PG(t *testing.T) {
	ctx := context.Background()
	pool := openDCIntegrationPool(t, ctx)
	base := time.Date(2026, 6, 29, 10, 0, 0, 0, time.UTC)
	tenantID, userID := seedDCTenantUser(t, ctx, pool)
	oldest := seedDCFamilies(t, ctx, pool, tenantID, userID, 2, base)

	svc := newDCService(pool, base)

	_, err := svc.Create(ctx, CreateInput{TenantID: tenantID, UserID: userID, IP: "10.0.0.1", UserAgent: "Chrome/1"})
	var confirmErr *DeviceConfirmationRequiredError
	if !errors.As(err, &confirmErr) {
		t.Fatalf("Create err=%v want *DeviceConfirmationRequiredError", err)
	}

	// pending 记录已落真表。
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM device_confirmations WHERE tenant_id=$1 AND token_hash=$2`,
		tenantID, HashDeviceConfirmationToken(confirmErr.RawToken)).Scan(&status); err != nil {
		t.Fatalf("read pending record: %v", err)
	}
	if status != "pending" {
		t.Fatalf("pending record status=%q want pending", status)
	}
	if got := countActiveFamiliesPG(t, ctx, pool, tenantID, userID); got != 2 {
		t.Fatalf("active before confirm=%d want 2", got)
	}

	if err := svc.ConfirmDevice(ctx, tenantID, confirmErr.RawToken); err != nil {
		t.Fatalf("ConfirmDevice: %v", err)
	}
	if got := countActiveFamiliesPG(t, ctx, pool, tenantID, userID); got != 1 {
		t.Fatalf("active after confirm=%d want 1 (oldest revoked)", got)
	}
	if s := familyStatusPG(t, ctx, pool, oldest); s != "revoked" {
		t.Fatalf("oldest family status=%q want revoked", s)
	}
	// 记录被标 confirmed。
	if err := pool.QueryRow(ctx,
		`SELECT status FROM device_confirmations WHERE tenant_id=$1 AND token_hash=$2`,
		tenantID, HashDeviceConfirmationToken(confirmErr.RawToken)).Scan(&status); err != nil {
		t.Fatalf("re-read record: %v", err)
	}
	if status != "confirmed" {
		t.Fatalf("record status after confirm=%q want confirmed", status)
	}
}

// TestDeviceConfirmation_ExpiredRejected_PG: 过期 token 拒绝, 不撤 family。
func TestDeviceConfirmation_ExpiredRejected_PG(t *testing.T) {
	ctx := context.Background()
	pool := openDCIntegrationPool(t, ctx)
	base := time.Date(2026, 6, 29, 11, 0, 0, 0, time.UTC)
	tenantID, userID := seedDCTenantUser(t, ctx, pool)
	seedDCFamilies(t, ctx, pool, tenantID, userID, 2, base)

	svc := newDCService(pool, base)
	svc.DeviceConfirmationTTL = time.Hour

	_, err := svc.Create(ctx, CreateInput{TenantID: tenantID, UserID: userID, IP: "10.0.0.2", UserAgent: "Chrome/1"})
	var confirmErr *DeviceConfirmationRequiredError
	if !errors.As(err, &confirmErr) {
		t.Fatalf("Create err=%v want *DeviceConfirmationRequiredError", err)
	}

	// 时钟推到 TTL 之后。
	svc.Now = func() time.Time { return base.Add(2 * time.Hour) }
	if err := svc.ConfirmDevice(ctx, tenantID, confirmErr.RawToken); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("ConfirmDevice expired err=%v want ErrTokenExpired", err)
	}
	if got := countActiveFamiliesPG(t, ctx, pool, tenantID, userID); got != 2 {
		t.Fatalf("active after expired confirm=%d want 2 (nothing revoked)", got)
	}
}

// TestDeviceConfirmation_ReplayIdempotent_PG: 二次 confirm 不重复撤 (真 PG 条件 UPDATE 幂等)。
func TestDeviceConfirmation_ReplayIdempotent_PG(t *testing.T) {
	ctx := context.Background()
	pool := openDCIntegrationPool(t, ctx)
	base := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	tenantID, userID := seedDCTenantUser(t, ctx, pool)
	// 3 个活跃, 上限 2 → 若重放误撤两次活跃数会掉到 0, 与正确的 1 区分明显。
	seedDCFamilies(t, ctx, pool, tenantID, userID, 3, base)
	svc := newDCService(pool, base)

	_, err := svc.Create(ctx, CreateInput{TenantID: tenantID, UserID: userID, IP: "10.0.0.3", UserAgent: "Chrome/1"})
	var confirmErr *DeviceConfirmationRequiredError
	if !errors.As(err, &confirmErr) {
		t.Fatalf("Create err=%v want *DeviceConfirmationRequiredError", err)
	}

	if err := svc.ConfirmDevice(ctx, tenantID, confirmErr.RawToken); err != nil {
		t.Fatalf("first ConfirmDevice: %v", err)
	}
	first := countActiveFamiliesPG(t, ctx, pool, tenantID, userID)
	if first != 2 {
		t.Fatalf("active after first confirm=%d want 2", first)
	}

	// 二次确认: 已 confirmed → 已用语义, 绝不再撤。
	second := svc.ConfirmDevice(ctx, tenantID, confirmErr.RawToken)
	if !errors.Is(second, ErrDeviceConfirmationNotFound) && !errors.Is(second, ErrRefreshReplay) {
		t.Fatalf("replay ConfirmDevice err=%v want already-consumed sentinel", second)
	}
	if got := countActiveFamiliesPG(t, ctx, pool, tenantID, userID); got != first {
		t.Fatalf("active after replay=%d want %d (replay must not revoke again)", got, first)
	}
}

// TestDeviceConfirmation_DormantNoSideEffects_PG: MaxActiveFamilies=0 时照常签发,
// 不落 pending, 不撤 family (默认零生产行为变更的护栏, 真 PG)。
func TestDeviceConfirmation_DormantNoSideEffects_PG(t *testing.T) {
	ctx := context.Background()
	pool := openDCIntegrationPool(t, ctx)
	base := time.Date(2026, 6, 29, 13, 0, 0, 0, time.UTC)
	tenantID, userID := seedDCTenantUser(t, ctx, pool)
	seedDCFamilies(t, ctx, pool, tenantID, userID, 5, base)

	svc := NewService(NewPostgresStore(pool))
	svc.SigningKey = testSigningKey()
	svc.Now = func() time.Time { return base }
	// 默认: MaxActiveFamilies=0 (休眠), 即便 DevicePolicy 误设 confirm。
	svc.DevicePolicy = "confirm"

	tokens, err := svc.Create(ctx, CreateInput{TenantID: tenantID, UserID: userID, IP: "10.0.0.4", UserAgent: "Chrome/1"})
	if err != nil {
		t.Fatalf("Create dormant should succeed: %v", err)
	}
	if tokens.SessionToken == "" {
		t.Fatal("dormant must still issue a session token")
	}
	// 5 原有 + 1 新建 = 6 活跃, 无撤。
	if got := countActiveFamiliesPG(t, ctx, pool, tenantID, userID); got != 6 {
		t.Fatalf("active families=%d want 6 (5 seeded + 1 new, none revoked)", got)
	}
	// 不落任何 pending 记录。
	var pendingCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM device_confirmations WHERE tenant_id=$1 AND user_id=$2`,
		tenantID, userID).Scan(&pendingCount); err != nil {
		t.Fatalf("count device_confirmations: %v", err)
	}
	if pendingCount != 0 {
		t.Fatalf("device_confirmations rows=%d want 0 (dormant must not create pending)", pendingCount)
	}
}
