//go:build integration_pg

package megroupshttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingcatalog"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionenforce"
)

func openMeGroupsPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := db.Open(ctx, db.PoolConfig{DSN: dsn, MaxConns: 4})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type meGroupsFixture struct {
	t       *testing.T
	ctx     context.Context
	pool    *pgxpool.Pool
	suffix  string
	tenants []int64
	tenant  int64
	user    int64
}

func newMeGroupsFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *meGroupsFixture {
	t.Helper()
	f := &meGroupsFixture{t: t, ctx: ctx, pool: pool, suffix: uuid.NewString()}
	f.tenant = f.seedTenant("me-groups")
	f.user = f.seedUser(f.tenant, "premium", "u")
	t.Cleanup(f.cleanup)
	return f
}

func (f *meGroupsFixture) seedTenant(label string) int64 {
	f.t.Helper()
	var id int64
	if err := f.pool.QueryRow(f.ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, label+"-"+f.suffix).Scan(&id); err != nil {
		f.t.Fatalf("seed tenant: %v", err)
	}
	f.tenants = append(f.tenants, id)
	return id
}

func (f *meGroupsFixture) seedUser(tenantID int64, userGroup, label string) int64 {
	f.t.Helper()
	var id int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO users (tenant_id, display_name, user_group) VALUES ($1, $2, $3) RETURNING id`,
		tenantID, "user-"+label+"-"+f.suffix, userGroup,
	).Scan(&id); err != nil {
		f.t.Fatalf("seed user: %v", err)
	}
	return id
}

func (f *meGroupsFixture) seedPoolGroup(tenantID int64, name string) int64 {
	f.t.Helper()
	var id int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO pool_groups (tenant_id, name, enabled) VALUES ($1, $2, true) RETURNING id`,
		tenantID, name+"-"+f.suffix,
	).Scan(&id); err != nil {
		f.t.Fatalf("seed pool group: %v", err)
	}
	return id
}

func (f *meGroupsFixture) seedRoute(tenantID, poolGroupID int64, userGroup, name string) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO routes (tenant_id, name, user_group_match, model_pattern_match, pool_group_id, enabled)
		 VALUES ($1, $2, $3, '*', $4, true)`,
		tenantID, name+"-"+f.suffix, userGroup, poolGroupID,
	); err != nil {
		f.t.Fatalf("seed route: %v", err)
	}
}

func (f *meGroupsFixture) seedRatio(tenantID, poolGroupID int64, ratio string, public bool) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO pool_group_pricing_ratios (tenant_id, pool_group_id, ratio, public_ratio, created_by, updated_by)
		 VALUES ($1, $2, $3::numeric(20,8), $4, 'test', 'test')`,
		tenantID, poolGroupID, ratio, public,
	); err != nil {
		f.t.Fatalf("seed ratio: %v", err)
	}
}

func (f *meGroupsFixture) cleanup() {
	ctx := context.Background()
	for _, tenantID := range f.tenants {
		_, _ = f.pool.Exec(ctx, `DELETE FROM pool_group_pricing_ratios WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM routes WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM pool_groups WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM users WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenantID)
	}
}

func (f *meGroupsFixture) handler() http.Handler {
	return NewHandler(Deps{
		Auth:       SessionResolver{},
		UserGroups: NewPostgresUserGroupReader(f.pool),
		RoutesRepo: subscriptionenforce.NewPostgresRoutesRepo(f.pool),
		Ratios:     pricingcatalog.NewPostgresStore(f.pool),
		Pools:      NewPostgresPoolNameLister(f.pool),
	})
}

func (f *meGroupsFixture) get() *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/me/groups", nil)
	ctx := auth.ContextWithSession(req.Context(), auth.SessionIdentity{TenantID: f.tenant, UserID: f.user})
	f.handler().ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

// TestMeGroupsPublicRatioVisibility 用一个真实的 premium tier 预置两个 pool
// group:一个 public-ratio group(其倍率必须浮现)和一个 private group
//(其倍率必须被隐去)。public-ratio 开关正是端到端、贯穿真实存储地守护
// 金钱/竞争情报披露的那个杠杆。
//
// 变异:把 public group 的 public_ratio seed 翻成 false(或移除 handler 的
// public 门控)。那样 ratio "2.00000000" 要么消失(seed 侧),要么隐藏的
// "9.90000000" 浮现(handler 侧)—— 两个断言都会转红。
func TestMeGroupsPublicRatioVisibility(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openMeGroupsPool(t, ctx)
	f := newMeGroupsFixture(t, ctx, pool)

	pubGroup := f.seedPoolGroup(f.tenant, "pub")
	privGroup := f.seedPoolGroup(f.tenant, "priv")
	f.seedRoute(f.tenant, pubGroup, "premium", "route-pub")
	f.seedRoute(f.tenant, privGroup, "premium", "route-priv")
	f.seedRatio(f.tenant, pubGroup, "2.0", true)
	f.seedRatio(f.tenant, privGroup, "9.9", false)

	rec := f.get()
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Object    string           `json:"object"`
		UserGroup string           `json:"user_group"`
		Items     []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if body.UserGroup != "premium" {
		t.Fatalf("user_group=%q want premium", body.UserGroup)
	}

	pub := findItem(body.Items, pubGroup)
	if pub == nil {
		t.Fatalf("public group %d missing: %v", pubGroup, body.Items)
	}
	if pub["ratio"] != "2.00000000" {
		t.Fatalf("public group ratio=%v want 2.00000000", pub["ratio"])
	}
	if pub["has_public_ratio"] != true {
		t.Fatalf("public group has_public_ratio=%v want true", pub["has_public_ratio"])
	}

	priv := findItem(body.Items, privGroup)
	if priv == nil {
		t.Fatalf("private group %d should still be listed: %v", privGroup, body.Items)
	}
	if _, present := priv["ratio"]; present {
		t.Fatalf("private group leaked ratio=%v", priv["ratio"])
	}
	if priv["has_public_ratio"] != false {
		t.Fatalf("private group has_public_ratio=%v want false", priv["has_public_ratio"])
	}
	if got := rec.Body.String(); contains(got, "9.90000000") {
		t.Fatalf("hidden internal multiplier leaked: %s", got)
	}
}

// TestMeGroupsTierAndTenantIsolation 针对真实的 routes JOIN 证明 tier 白名单
// 与 tenant 隔离:第二个 tenant 的 group,以及同一 tenant 内异 tier 的 group,
// 二者都必须不可见。
//
// 变异:去掉 GroupRoutes 过滤(列出所有已定价 group)—— 异 tier group 浮现;
// 或信任请求里的 tenant —— tenant 2 的 group 浮现。无论哪种,缺席断言都会转红。
func TestMeGroupsTierAndTenantIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := openMeGroupsPool(t, ctx)
	f := newMeGroupsFixture(t, ctx, pool)

	// 允许:premium tier 的 group。
	allowed := f.seedPoolGroup(f.tenant, "allowed")
	f.seedRoute(f.tenant, allowed, "premium", "route-allowed")
	f.seedRatio(f.tenant, allowed, "1.5", true)

	// 同一 tenant,不同 tier(basic)—— 对 premium 用户绝不能出现。
	foreignTier := f.seedPoolGroup(f.tenant, "basic")
	f.seedRoute(f.tenant, foreignTier, "basic", "route-basic")
	f.seedRatio(f.tenant, foreignTier, "3.3", true)

	// 完全不同的 tenant —— 绝不能出现。
	otherTenant := f.seedTenant("other")
	otherGroup := f.seedPoolGroup(otherTenant, "other-secret")
	f.seedRoute(otherTenant, otherGroup, "premium", "route-other")
	f.seedRatio(otherTenant, otherGroup, "7.7", true)

	rec := f.get()
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if findItem(body.Items, allowed) == nil {
		t.Fatalf("allowed premium group %d missing: %v", allowed, body.Items)
	}
	if findItem(body.Items, foreignTier) != nil {
		t.Fatalf("foreign-tier group %d leaked: %v", foreignTier, body.Items)
	}
	if findItem(body.Items, otherGroup) != nil {
		t.Fatalf("cross-tenant group %d leaked: %v", otherGroup, body.Items)
	}
}

func findItem(items []map[string]any, id int64) map[string]any {
	for _, it := range items {
		if v, ok := it["pool_group_id"].(float64); ok && int64(v) == id {
			return it
		}
	}
	return nil
}
