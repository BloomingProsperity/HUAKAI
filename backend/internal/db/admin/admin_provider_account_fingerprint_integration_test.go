//go:build integration_pg

package admin

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestUpdateProviderAccountFingerprintProfile_BindUnbindAndCrossTenant 真 PG 验证手写 sqlc 查询:
//   - 绑定:把账号的 tls_fingerprint_profile_id 设成同租户 profile;
//   - 解绑:ProfileID=nil → 清回 NULL(内置默认);
//   - 跨租户:把 A 租户账号绑到 B 租户 profile → DB 触发器(0038)RAISE P0001 拒绝。
// 这是 sqlc 手改后替代重生成保证的真 PG 验证;同时坐实 handler 把 P0001 映射成 400 的依据。
func TestUpdateProviderAccountFingerprintProfile_BindUnbindAndCrossTenant(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openAdminAuditIntegrationPool(t, ctx)
	q := New(pool)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenantA, accountA := seedClearRateLimitAccount(t, ctx, pool, suffix+"-a")
	t.Cleanup(func() { cleanupAdminProviderAccountHealthGraph(t, context.Background(), pool, tenantA) })

	var profileA int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tls_fingerprint_profiles (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		tenantA, "fp-a-"+suffix,
	).Scan(&profileA); err != nil {
		t.Fatalf("seed profile A: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM tls_fingerprint_profiles WHERE tenant_id=$1`, tenantA) })

	actor := "admin:fp-bind"
	readFK := func() *int64 {
		var got *int64
		if err := pool.QueryRow(ctx, `SELECT tls_fingerprint_profile_id FROM provider_accounts WHERE id=$1`, accountA).Scan(&got); err != nil {
			t.Fatalf("re-select FK: %v", err)
		}
		return got
	}

	// 绑定同租户 profile → FK 被设上。
	if err := q.UpdateProviderAccountFingerprintProfile(ctx, UpdateProviderAccountFingerprintProfileParams{
		ProfileID: &profileA, ActorID: &actor, ID: accountA, TenantID: tenantA,
	}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if got := readFK(); got == nil || *got != profileA {
		t.Fatalf("bind 后 FK 应=%d,实得 %v", profileA, got)
	}

	// 解绑(nil)→ FK 清回 NULL。
	if err := q.UpdateProviderAccountFingerprintProfile(ctx, UpdateProviderAccountFingerprintProfileParams{
		ProfileID: nil, ActorID: &actor, ID: accountA, TenantID: tenantA,
	}); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	if got := readFK(); got != nil {
		t.Fatalf("unbind 后 FK 应为 NULL,实得 %v", *got)
	}

	// 跨租户:A 租户账号绑 B 租户 profile → DB 触发器拒绝(P0001)。
	tenantB, _ := seedClearRateLimitAccount(t, ctx, pool, suffix+"-b")
	t.Cleanup(func() { cleanupAdminProviderAccountHealthGraph(t, context.Background(), pool, tenantB) })
	var profileB int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO tls_fingerprint_profiles (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		tenantB, "fp-b-"+suffix,
	).Scan(&profileB); err != nil {
		t.Fatalf("seed profile B: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM tls_fingerprint_profiles WHERE tenant_id=$1`, tenantB) })

	err := q.UpdateProviderAccountFingerprintProfile(ctx, UpdateProviderAccountFingerprintProfileParams{
		ProfileID: &profileB, ActorID: &actor, ID: accountA, TenantID: tenantA,
	})
	if err == nil {
		t.Fatal("跨租户 profile 绑定应被 DB 触发器拒绝,实得 nil error")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "P0001" {
		t.Fatalf("跨租户绑定应返回触发器 P0001,实得 %v", err)
	}
	// 被拒后 A 账号 FK 仍为 NULL(未被污染)。
	if got := readFK(); got != nil {
		t.Fatalf("跨租户被拒后 FK 不应被改,实得 %v", *got)
	}
}
