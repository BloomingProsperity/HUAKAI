// HUAKAI · iKun
//go:build integration_pg

package subscriptionenforce

import (
	"context"
	"os"
	"testing"
	"time"

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
	return seedPoolGroupEx(t, ctx, pool, tenantID, name, true, false)
}

// seedPoolGroupEx 可指定 enabled 与是否软删, 用于 F4 排除无效目标池的判别测。
func seedPoolGroupEx(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, name string, enabled, deleted bool) int64 {
	t.Helper()
	var deletedAt *time.Time
	if deleted {
		tnow := time.Now()
		deletedAt = &tnow
	}
	var id int64
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name, enabled, deleted_at) VALUES ($1,$2,$3,$4) RETURNING id`,
		tenantID, name, enabled, deletedAt).Scan(&id); err != nil {
		t.Fatalf("seed pool_group: %v", err)
	}
	return id
}

func seedRoute(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, name, userGroup, modelPattern string, poolGroupID int64, enabled bool) {
	t.Helper()
	seedRouteEx(t, ctx, pool, tenantID, name, userGroup, modelPattern, poolGroupID, enabled, false)
}

// seedRouteEx 可指定是否软删路由, 用于 F4 软删谓词判别测。
func seedRouteEx(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, name, userGroup, modelPattern string, poolGroupID int64, enabled, deleted bool) {
	t.Helper()
	var deletedAt *time.Time
	if deleted {
		tnow := time.Now()
		deletedAt = &tnow
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO routes (tenant_id, name, user_group_match, model_pattern_match, pool_group_id, enabled, deleted_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		tenantID, name, userGroup, modelPattern, poolGroupID, enabled, deletedAt); err != nil {
		t.Fatalf("seed route: %v", err)
	}
}

// seedRoutePrio 显式设 match_priority, 用于 slice B 优先档收窄判别测(默认 helper 走 DB 默认 100)。
func seedRoutePrio(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, name, userGroup, modelPattern string, poolGroupID int64, priority int) {
	t.Helper()
	seedRoutePrioEx(t, ctx, pool, tenantID, name, userGroup, modelPattern, poolGroupID, priority, false)
}

// seedRoutePrioEx 显式设 match_priority + 是否软删, 用于"高优先档软删须回退次档"的判别测。
func seedRoutePrioEx(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID int64, name, userGroup, modelPattern string, poolGroupID int64, priority int, deleted bool) {
	t.Helper()
	var deletedAt *time.Time
	if deleted {
		tnow := time.Now()
		deletedAt = &tnow
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO routes (tenant_id, name, user_group_match, model_pattern_match, pool_group_id, match_priority, enabled, deleted_at)
VALUES ($1,$2,$3,$4,$5,$6,true,$7)`,
		tenantID, name, userGroup, modelPattern, poolGroupID, priority, deletedAt); err != nil {
		t.Fatalf("seed route(prio,ex): %v", err)
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

func cleanupTenants(pool *pgxpool.Pool, ids ...int64) func() {
	return func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM routes WHERE tenant_id = ANY($1)`, ids)
		_, _ = pool.Exec(c, `DELETE FROM pool_groups WHERE tenant_id = ANY($1)`, ids)
		_, _ = pool.Exec(c, `DELETE FROM tenants WHERE id = ANY($1)`, ids)
	}
}

// TestPG_GroupRoutes 守 routes 查询的 WHERE 范围 + model_pattern 过滤 + Configured 语义。
// 判别:
//   - 漏 tenant_id 谓词 → 跨租户 pgOther 泄入 premium 集 → assertSet 变红 (串租户路由 = 安全)。
//   - 漏 enabled 谓词 → 禁用路由 pgDisabled 泄入 → 红。
//   - model_pattern 过滤错 → claude 请求拿到 gpt-only 的 pgB / 反之 → 红。
//   - gemini(无命中)但 Configured 应为 true(premium 配了路由)→ 白名单据此对越档拒(F1)。
func TestPG_GroupRoutes(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)
	suffix := uuid.NewString()

	tenantID := seedTenant(t, ctx, pool, "se-"+suffix)
	otherTenantID := seedTenant(t, ctx, pool, "se-other-"+suffix)
	t.Cleanup(cleanupTenants(pool, tenantID, otherTenantID))

	pgA := seedPoolGroup(t, ctx, pool, tenantID, "premiumA-"+suffix)
	pgB := seedPoolGroup(t, ctx, pool, tenantID, "premiumB-"+suffix)
	pgC := seedPoolGroup(t, ctx, pool, tenantID, "defaultC-"+suffix)
	pgDisabled := seedPoolGroup(t, ctx, pool, tenantID, "disabled-"+suffix)
	pgOther := seedPoolGroup(t, ctx, pool, otherTenantID, "other-"+suffix)

	seedRoute(t, ctx, pool, tenantID, "r-claude-"+suffix, "premium", "claude-*", pgA, true)
	seedRoute(t, ctx, pool, tenantID, "r-gpt-"+suffix, "premium", "gpt-4o", pgB, true)
	seedRoute(t, ctx, pool, tenantID, "r-def-"+suffix, "default", "*", pgC, true)
	seedRoute(t, ctx, pool, tenantID, "r-disabled-"+suffix, "premium", "*", pgDisabled, false) // 禁用路由
	seedRoute(t, ctx, pool, otherTenantID, "r-other-"+suffix, "premium", "*", pgOther, true)   // 跨租户

	repo := NewPostgresRoutesRepo(pool)

	// premium + claude 模型 → 只 premiumA (claude-* 命中; gpt-4o 不命中; 禁用/跨租户排除)。
	got, err := repo.GroupRoutes(ctx, tenantID, "premium", "claude-3-5-sonnet")
	if err != nil {
		t.Fatalf("query claude: %v", err)
	}
	assertSet(t, got.Allowed, pgA)
	if !got.Configured {
		t.Fatal("premium 配了有效路由, Configured 应为 true")
	}

	// premium + gpt-4o → 只 premiumB (精确命中)。
	got, err = repo.GroupRoutes(ctx, tenantID, "premium", "gpt-4o")
	if err != nil {
		t.Fatalf("query gpt: %v", err)
	}
	assertSet(t, got.Allowed, pgB)

	// premium + 无任何 pattern 命中的模型 → Allowed 空, 但 Configured=true (白名单据此越档拒)。
	got, err = repo.GroupRoutes(ctx, tenantID, "premium", "gemini-2-pro")
	if err != nil {
		t.Fatalf("query gemini: %v", err)
	}
	assertSet(t, got.Allowed)
	if !got.Configured {
		t.Fatal("premium 有 claude/gpt 路由但本 model 未命中: Configured 必须仍为 true(白名单越档拒的前提)")
	}

	// default 档 + 任意模型 → 只 defaultC ('*' 全匹配), 不含 premium 的池。
	got, err = repo.GroupRoutes(ctx, tenantID, "default", "claude-3-5-sonnet")
	if err != nil {
		t.Fatalf("query default: %v", err)
	}
	assertSet(t, got.Allowed, pgC)
}

// TestPG_GroupRoutes_ExcludesInvalidTargets 守 F2/F4: JOIN pool_groups 排除目标池为
// 已禁用/软删的路由, routes.deleted_at 谓词排除软删路由; 这些既不进 Allowed 也不让
// Configured 误真。
// 判别:
//   - 漏 routes.deleted_at IS NULL → 软删路由 r-softroute 泄入 → Allowed 多一项 → 红。
//   - 漏 JOIN pg.enabled → 目标池禁用的路由泄入 → 红。
//   - 漏 JOIN pg.deleted_at IS NULL → 目标池软删的路由泄入 → 红。
//   - Configured 误把无效路由也计真 → gold(唯一路由软删)的 Configured 断言红。
func TestPG_GroupRoutes_ExcludesInvalidTargets(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)
	suffix := uuid.NewString()

	tenantID := seedTenant(t, ctx, pool, "se-inv-"+suffix)
	otherTenantID := seedTenant(t, ctx, pool, "se-inv-other-"+suffix)
	t.Cleanup(cleanupTenants(pool, tenantID, otherTenantID))

	pgValid := seedPoolGroupEx(t, ctx, pool, tenantID, "valid-"+suffix, true, false)
	// pgCrossTenant: 有效池但属于另一租户 — 当前租户 route 指向它须被 JOIN 的
	// pg.tenant_id = r.tenant_id 排除。
	pgCrossTenant := seedPoolGroupEx(t, ctx, pool, otherTenantID, "crosspg-"+suffix, true, false)
	pgDisabledTarget := seedPoolGroupEx(t, ctx, pool, tenantID, "disabledpg-"+suffix, false, false) // 目标池禁用
	pgDeletedTarget := seedPoolGroupEx(t, ctx, pool, tenantID, "deletedpg-"+suffix, true, true)     // 目标池软删

	seedRouteEx(t, ctx, pool, tenantID, "r-valid-"+suffix, "premium", "*", pgValid, true, false)
	seedRouteEx(t, ctx, pool, tenantID, "r-crosstgt-"+suffix, "premium", "*", pgCrossTenant, true, false)       // 当前租户 route 指向他租户池
	seedRouteEx(t, ctx, pool, tenantID, "r-softroute-"+suffix, "premium", "*", pgValid, true, true)             // 软删路由
	seedRouteEx(t, ctx, pool, tenantID, "r-disabledtgt-"+suffix, "premium", "*", pgDisabledTarget, true, false) // 目标池禁用
	seedRouteEx(t, ctx, pool, tenantID, "r-deletedtgt-"+suffix, "premium", "*", pgDeletedTarget, true, false)   // 目标池软删

	repo := NewPostgresRoutesRepo(pool)

	got, err := repo.GroupRoutes(ctx, tenantID, "premium", "claude-3-5-sonnet")
	if err != nil {
		t.Fatalf("query premium: %v", err)
	}
	// 只有 pgValid 进集: 软删路由 / 目标池禁用 / 目标池软删 全排除。
	assertSet(t, got.Allowed, pgValid)
	if !got.Configured {
		t.Fatal("premium 有一条有效路由(pgValid), Configured 应为 true")
	}

	// gold 档唯一路由被软删 → 视同未配置: Configured=false (越档时该放行而非拒)。
	seedRouteEx(t, ctx, pool, tenantID, "r-gold-soft-"+suffix, "gold", "*", pgValid, true, true)
	gold, err := repo.GroupRoutes(ctx, tenantID, "gold", "claude-3-5-sonnet")
	if err != nil {
		t.Fatalf("query gold: %v", err)
	}
	if gold.Configured {
		t.Fatal("gold 唯一路由已软删: Configured 应为 false(该档视同未配置分组路由→放行)")
	}
	assertSet(t, gold.Allowed)

	// silver 档唯一路由指向他租户 pool → 被 JOIN 的 pg.tenant_id 排除, 视同未配置:
	// Configured=false。这是 tenant isolation 安全关键判别。
	// mutation: 删 SQL 的 `AND pg.tenant_id = r.tenant_id` → silver 会 Configured=true,
	// 且上面 premium 的 Allowed 会泄入 pgCrossTenant → 两处断言红。
	seedRouteEx(t, ctx, pool, tenantID, "r-silver-cross-"+suffix, "silver", "*", pgCrossTenant, true, false)
	silver, err := repo.GroupRoutes(ctx, tenantID, "silver", "claude-3-5-sonnet")
	if err != nil {
		t.Fatalf("query silver: %v", err)
	}
	if silver.Configured {
		t.Fatal("silver 唯一路由指向他租户 pool: Configured 应为 false(跨租户目标池被 JOIN 排除)")
	}
	assertSet(t, silver.Allowed)
}

// TestPG_GroupRoutes_PriorityArbitration 守 slice B 真裁决: 多条命中本 model 时只放最高优先档
// (最小 match_priority)的 pool, 并列同档取并集, 全默认值退化为全量(向后兼容), 最高档软删则回退次档。
// 判别:
//   - 收窄逻辑取最大值而非最小 → premium 期望 {pgHi} 变 {pgLo} → 红。
//   - 并列同档漏池 → gold/silver 并集断言 → 红。
//   - 误把全默认配置也收成子集 → gold 向后兼容断言 → 红。
//   - 最高档软删未回退(仍认为最高档存在/收成空) → bronze 回退断言 → 红。
func TestPG_GroupRoutes_PriorityArbitration(t *testing.T) {
	ctx := context.Background()
	pool := openPool(t, ctx)
	suffix := uuid.NewString()

	tenantID := seedTenant(t, ctx, pool, "se-prio-"+suffix)
	t.Cleanup(cleanupTenants(pool, tenantID))

	pgHi := seedPoolGroup(t, ctx, pool, tenantID, "hi-"+suffix)
	pgLo := seedPoolGroup(t, ctx, pool, tenantID, "lo-"+suffix)
	pgA := seedPoolGroup(t, ctx, pool, tenantID, "a-"+suffix)
	pgB := seedPoolGroup(t, ctx, pool, tenantID, "b-"+suffix)

	repo := NewPostgresRoutesRepo(pool)

	// premium: 两条都命中 claude-*, priority 10(pgHi) 与 20(pgLo) → 只放最高档 pgHi。
	seedRoutePrio(t, ctx, pool, tenantID, "p-hi-"+suffix, "premium", "claude-*", pgHi, 10)
	seedRoutePrio(t, ctx, pool, tenantID, "p-lo-"+suffix, "premium", "claude-*", pgLo, 20)
	premium, err := repo.GroupRoutes(ctx, tenantID, "premium", "claude-3-5-sonnet")
	if err != nil {
		t.Fatalf("query premium: %v", err)
	}
	assertSet(t, premium.Allowed, pgHi) // 收窄: pgLo(低优先档)被排除
	if !premium.Configured {
		t.Fatal("premium 有有效路由, Configured 应为 true")
	}

	// gold: 两条都命中且都默认优先级(同档) → 取并集(向后兼容, 与旧全量集相等)。
	seedRoutePrio(t, ctx, pool, tenantID, "g-a-"+suffix, "gold", "claude-*", pgA, 100)
	seedRoutePrio(t, ctx, pool, tenantID, "g-b-"+suffix, "gold", "claude-*", pgB, 100)
	gold, err := repo.GroupRoutes(ctx, tenantID, "gold", "claude-3-5-sonnet")
	if err != nil {
		t.Fatalf("query gold: %v", err)
	}
	assertSet(t, gold.Allowed, pgA, pgB) // 同档并集, 不收窄

	// silver: 三条命中 5(pgHi)/5(pgLo)/20(pgA) → 最高档(5)两池并集 {pgHi,pgLo}, pgA(20)排除。
	seedRoutePrio(t, ctx, pool, tenantID, "s-1-"+suffix, "silver", "claude-*", pgHi, 5)
	seedRoutePrio(t, ctx, pool, tenantID, "s-2-"+suffix, "silver", "claude-*", pgLo, 5)
	seedRoutePrio(t, ctx, pool, tenantID, "s-3-"+suffix, "silver", "claude-*", pgA, 20)
	silver, err := repo.GroupRoutes(ctx, tenantID, "silver", "claude-3-5-sonnet")
	if err != nil {
		t.Fatalf("query silver: %v", err)
	}
	assertSet(t, silver.Allowed, pgHi, pgLo)
	// Configured 独立于优先档收窄(contract #5): 收窄后仍 true。
	// mutation: 把 Configured 绑 len(matched)/len(Allowed) → silver 收窄了仍须 true → 此断言守门。
	if !silver.Configured {
		t.Fatal("silver 有有效路由, Configured 须为 true(独立于优先档收窄)")
	}

	// bronze: 最高档(5, pgHi)被软删 → SQL deleted_at 谓词过滤后, 档计算回退到次档(20, pgA)。
	// 关键: 软删那条 priority(5) 比存活那条(20)更高优先, 故只有软删谓词真生效才会回退到 pgA;
	// 若软删谓词坏掉, pgHi(5) 会胜出 → Allowed={pgHi} → 断言红(判别软删与档计算的交互)。
	seedRoutePrioEx(t, ctx, pool, tenantID, "b-hi-"+suffix, "bronze", "claude-*", pgHi, 5, true)
	seedRoutePrio(t, ctx, pool, tenantID, "b-lo-"+suffix, "bronze", "claude-*", pgA, 20)
	bronze, err := repo.GroupRoutes(ctx, tenantID, "bronze", "claude-3-5-sonnet")
	if err != nil {
		t.Fatalf("query bronze: %v", err)
	}
	assertSet(t, bronze.Allowed, pgA) // 软删的高档不参与, 回退到 pgA
	// bronze 仍有一条有效路由(pgA), Configured 须为 true(回退/收窄不改 Configured)。
	if !bronze.Configured {
		t.Fatal("bronze 有有效路由(pgA), Configured 须为 true")
	}
}
