// HUAKAI · iKun
//go:build integration_pg

package subscriptionenforce

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func openPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	return id
}

func seedPoolGroup(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, name string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1,$2) RETURNING id`, tenantID, name).Scan(&id); err != nil {
		t.Fatalf("seed pool_group: %v", err)
	}
	return id
}

func seedRoute(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, name, userGroup, modelPattern string, poolGroupID int64, enabled bool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
INSERT INTO routes (tenant_id, name, user_group_match, model_pattern_match, pool_group_id, enabled)
VALUES ($1,$2,$3,$4,$5,$6)`,
		tenantID, name, userGroup, modelPattern, poolGroupID, enabled); err != nil {
		t.Fatalf("seed route: %v", err)
	}
}

func assertSet(t *testing.T, got map[int64]struct{}, want ...int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("set size = %d (%v), want %d (%v)", len(got), setKeys(got), len(want), want)
	}
	for _, w := range want {
		if _, ok := got[w]; !ok {
			t.Fatalf("expected pool_group %d in %v", w, setKeys(got))
		}
	}
}

func setKeys(m map[int64]struct{}) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestPG_AllowedPoolGroups 守 routes 查询的 WHERE 范围 + model_pattern 过滤。
// 判别:
//   - 漏 tenant_id 谓词 → 跨租户 pgOther 泄入 premium 集 → assertSet 变红 (串租户路由 = 安全)。
//   - 漏 enabled 谓词 → 禁用路由 pgDisabled 泄入 → 红。
//   - model_pattern 过滤错 → claude 请求拿到 gpt-only 的 pgB / 反之 → 红。
func TestPG_AllowedPoolGroups(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)
	suffix := uuid.NewString()

	tenantID := seedTenant(t, ctx, pool, "se-"+suffix)
	otherTenantID := seedTenant(t, ctx, pool, "se-other-"+suffix)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM routes WHERE tenant_id IN ($1,$2)`, tenantID, otherTenantID)
		_, _ = pool.Exec(c, `DELETE FROM pool_groups WHERE tenant_id IN ($1,$2)`, tenantID, otherTenantID)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id IN ($1,$2)`, tenantID, otherTenantID)
	})

	pgA := seedPoolGroup(t, ctx, pool, tenantID, "premiumA-"+suffix)
	pgB := seedPoolGroup(t, ctx, pool, tenantID, "premiumB-"+suffix)
	pgC := seedPoolGroup(t, ctx, pool, tenantID, "defaultC-"+suffix)
	pgDisabled := seedPoolGroup(t, ctx, pool, tenantID, "disabled-"+suffix)
	pgOther := seedPoolGroup(t, ctx, pool, otherTenantID, "other-"+suffix)

	seedRoute(t, ctx, pool, tenantID, "r-claude-"+suffix, "premium", "claude-*", pgA, true)
	seedRoute(t, ctx, pool, tenantID, "r-gpt-"+suffix, "premium", "gpt-4o", pgB, true)
	seedRoute(t, ctx, pool, tenantID, "r-def-"+suffix, "default", "*", pgC, true)
	seedRoute(t, ctx, pool, tenantID, "r-disabled-"+suffix, "premium", "*", pgDisabled, false) // 禁用
	seedRoute(t, ctx, pool, otherTenantID, "r-other-"+suffix, "premium", "*", pgOther, true)   // 跨租户

	repo := NewPostgresRoutesRepo(pool)

	// premium + claude 模型 → 只 premiumA (claude-* 命中; gpt-4o 不命中; 禁用/跨租户排除)。
	got, err := repo.AllowedPoolGroups(ctx, tenantID, "premium", "claude-3-5-sonnet")
	if err != nil {
		t.Fatalf("query claude: %v", err)
	}
	assertSet(t, got, pgA)

	// premium + gpt-4o → 只 premiumB (精确命中)。
	got, err = repo.AllowedPoolGroups(ctx, tenantID, "premium", "gpt-4o")
	if err != nil {
		t.Fatalf("query gpt: %v", err)
	}
	assertSet(t, got, pgB)

	// premium + 无任何 pattern 命中的模型 → 空集 (调用方据此放行)。
	got, err = repo.AllowedPoolGroups(ctx, tenantID, "premium", "gemini-2-pro")
	if err != nil {
		t.Fatalf("query gemini: %v", err)
	}
	assertSet(t, got)

	// default 档 + 任意模型 → 只 defaultC ('*' 全匹配), 不含 premium 的池。
	got, err = repo.AllowedPoolGroups(ctx, tenantID, "default", "claude-3-5-sonnet")
	if err != nil {
		t.Fatalf("query default: %v", err)
	}
	assertSet(t, got, pgC)
}
