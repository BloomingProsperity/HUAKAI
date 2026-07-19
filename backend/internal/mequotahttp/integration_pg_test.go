//go:build integration_pg

package mequotahttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
)

func openMeQuotaPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
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

type meQuotaFixture struct {
	t       *testing.T
	ctx     context.Context
	pool    *pgxpool.Pool
	suffix  string
	tenants []int64
	tenantA int64
	userA   int64
	userB   int64
}

func newMeQuotaFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *meQuotaFixture {
	t.Helper()
	f := &meQuotaFixture{t: t, ctx: ctx, pool: pool, suffix: uuid.NewString()}
	f.tenantA = f.seedTenant("me-quota-a")
	f.userA = f.seedUser(f.tenantA, "a")
	f.userB = f.seedUser(f.tenantA, "b")
	t.Cleanup(f.cleanup)
	return f
}

func (f *meQuotaFixture) seedTenant(label string) int64 {
	f.t.Helper()
	var tenantID int64
	if err := f.pool.QueryRow(f.ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, label+"-"+f.suffix).Scan(&tenantID); err != nil {
		f.t.Fatalf("seed tenant: %v", err)
	}
	f.tenants = append(f.tenants, tenantID)
	return tenantID
}

func (f *meQuotaFixture) seedUser(tenantID int64, label string) int64 {
	f.t.Helper()
	var userID int64
	if err := f.pool.QueryRow(f.ctx,
		`INSERT INTO users (tenant_id, display_name) VALUES ($1, $2) RETURNING id`,
		tenantID, "user-"+label+"-"+f.suffix,
	).Scan(&userID); err != nil {
		f.t.Fatalf("seed user: %v", err)
	}
	return userID
}

func (f *meQuotaFixture) seedQuotaWindow(tenantID, userID int64, at time.Time, limit, reserved, settled string) {
	f.t.Helper()
	f.seedQuotaWindowMetric(tenantID, userID, "cost_usd", at, limit, reserved, settled)
}

func (f *meQuotaFixture) seedQuotaWindowMetric(tenantID, userID int64, metric string, at time.Time, limit, reserved, settled string) {
	f.t.Helper()
	f.seedScopeQuotaWindowMetric(tenantID, "user", strconv.FormatInt(userID, 10), metric, at, limit, reserved, settled)
}

func (f *meQuotaFixture) seedScopeQuotaWindowMetric(tenantID int64, scopeKind, scopeID, metric string, at time.Time, limit, reserved, settled string) {
	f.seedScopeQuotaWindowForModel(tenantID, scopeKind, scopeID, quota.ModelSelectorAll, metric, at, limit, reserved, settled)
}

func (f *meQuotaFixture) seedScopeQuotaWindowForModel(tenantID int64, scopeKind, scopeID, modelSelector, metric string, at time.Time, limit, reserved, settled string) {
	f.t.Helper()
	var policyID int64
	if err := f.pool.QueryRow(f.ctx, `
INSERT INTO quota_policies (
	tenant_id, scope_kind, scope_id, model_selector, metric, window_kind, window_seconds,
	limit_value, burst_value, mode, priority, enabled, valid_from, valid_until
) VALUES (
	$1, $2, $3, $4, $5, 'calendar_day', 0,
	$6::numeric(20,8), 0, 'enforce', 10, true, $7, $8
) RETURNING id`, tenantID, scopeKind, scopeID, modelSelector, metric, limit, at.Add(-time.Hour), at.Add(24*time.Hour)).Scan(&policyID); err != nil {
		f.t.Fatalf("seed quota policy (%s/%s/%s): %v", scopeKind, modelSelector, metric, err)
	}
	start, end, ok := quota.ComputeWindow(quota.WindowCalendarDay, 0, at)
	if !ok {
		f.t.Fatal("calendar day window did not compute")
	}
	if _, err := f.pool.Exec(f.ctx, `
INSERT INTO quota_windows (
	tenant_id, policy_id, window_start, window_end, reserved_value,
	settled_value, overage_value, request_count
) VALUES (
	$1, $2, $3, $4, $5::numeric(20,8),
	$6::numeric(20,8), 0, 9
)`, tenantID, policyID, start, end, reserved, settled); err != nil {
		f.t.Fatalf("seed quota window (%s/%s): %v", scopeKind, metric, err)
	}
}

func TestQuotaModelWindowProjectionAndLegacyAggregationIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openMeQuotaPool(t, ctx)
	f := newMeQuotaFixture(t, ctx, pool)
	at := time.Now().UTC()
	scopeID := strconv.FormatInt(f.userA, 10)
	f.seedScopeQuotaWindowForModel(f.tenantA, "user", scopeID, quota.ModelSelectorAll, "cost_usd", at, "10", "0", "3.5")
	f.seedScopeQuotaWindowForModel(f.tenantA, "user", scopeID, "gpt-4.1", "cost_usd", at, "2", "0", "1")

	store := quota.NewPostgresStore(pool)
	legacy, err := store.ListCurrentWindowsForScope(ctx, f.tenantA, quota.ScopeUser, scopeID, at)
	if err != nil {
		t.Fatalf("legacy cost read: %v", err)
	}
	if len(legacy) != 1 || legacy[0].ModelSelector != quota.ModelSelectorAll || legacy[0].SettledValue.String() != "3.5" {
		t.Fatalf("legacy windows=%+v; want wildcard cost row only", legacy)
	}

	detailed, err := store.ListCurrentWindowsForScopeMetrics(ctx, f.tenantA, quota.ScopeUser, scopeID, at, []quota.Metric{quota.MetricCostUSD})
	if err != nil {
		t.Fatalf("detailed model read: %v", err)
	}
	if len(detailed) != 2 || detailed[0].ModelSelector != quota.ModelSelectorAll || detailed[1].ModelSelector != "gpt-4.1" {
		t.Fatalf("detailed windows=%+v; want wildcard then exact", detailed)
	}
}

func (f *meQuotaFixture) cleanup() {
	ctx := context.Background()
	for _, tenantID := range f.tenants {
		_, _ = f.pool.Exec(ctx, `DELETE FROM quota_windows WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM quota_policies WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM users WHERE tenant_id=$1`, tenantID)
		_, _ = f.pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenantID)
	}
}

func TestMeQuotaSelfScope(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openMeQuotaPool(t, ctx)
	f := newMeQuotaFixture(t, ctx, pool)
	at := time.Now().UTC()
	f.seedQuotaWindow(f.tenantA, f.userA, at, "10", "3", "2")
	f.seedQuotaWindow(f.tenantA, f.userB, at, "99", "0", "0")
	h := NewHandler(Deps{
		Auth:  authStub{identity: auth.Identity{TenantID: f.tenantA, UserID: f.userA}},
		Store: quota.NewPostgresStore(pool),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/me/quota?user_id="+strconv.FormatInt(f.userB, 10)+"&scope_id="+strconv.FormatInt(f.userB, 10), nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	// 变异:handler 硬编码 userB 的 scopeID、传 "",或接受 query 里的 scope;
	// user A 就会看到 userB/全部窗口,此断言转红。
	if len(body.Items) != 1 {
		t.Fatalf("items len=%d want only user A quota window; body=%s", len(body.Items), rec.Body.String())
	}
	item := body.Items[0]
	if item["cap"] != "10" || item["remaining"] != "5" {
		t.Fatalf("cap/remaining=%v/%v want 10/5 body=%s", item["cap"], item["remaining"], rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"99"`) {
		t.Fatalf("response leaked user B quota window: %s", rec.Body.String())
	}
}

// TestMeQuotaMultiMetricWindows_Integration 是针对真实 DB 的端到端 F-OPS-001
// 证明:一个带有 requests + cost_usd + tokens_estimated 策略的用户能看到全部
// 三个窗口、各按 metric 标记。它还守护「不能偏移」:订阅/key-control 所依赖的
// cost-only store 方法,在其它策略也存在时仍只返回 cost_usd。
// 变异:把查询过滤回退为单个 metric -> 多 metric 读取返回 <3 -> 转红;
//
//	放宽 cost-only 方法的 metric 集合 -> 它返回 >1 -> 转红。
func TestMeQuotaMultiMetricWindows_Integration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openMeQuotaPool(t, ctx)
	f := newMeQuotaFixture(t, ctx, pool)
	at := time.Now().UTC()
	f.seedQuotaWindowMetric(f.tenantA, f.userA, "requests", at, "1000", "0", "120")
	f.seedQuotaWindowMetric(f.tenantA, f.userA, "cost_usd", at, "10", "0", "3.5")
	f.seedQuotaWindowMetric(f.tenantA, f.userA, "tokens_estimated", at, "1000000", "0", "45000")

	// 端到端:handler 返回全部三个窗口,各按其 metric 标记。
	h := NewHandler(Deps{
		Auth:  authStub{identity: auth.Identity{TenantID: f.tenantA, UserID: f.userA}},
		Store: quota.NewPostgresStore(pool),
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/me/quota?scope_id="+strconv.FormatInt(f.userA, 10), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	gotCap := map[string]any{}
	for _, it := range body.Items {
		m, _ := it["metric"].(string)
		gotCap[m] = it["cap"]
	}
	for metric, wantCap := range map[string]string{"requests": "1000", "cost_usd": "10", "tokens_estimated": "1000000"} {
		if gotCap[metric] != wantCap {
			t.Fatalf("metric %s cap=%v want %q; body=%s", metric, gotCap[metric], wantCap, rec.Body.String())
		}
	}

	// 不能偏移:cost-only store 方法(订阅进度 + key-control)即便现在存在
	// requests/tokens 策略,也必须仍只返回 cost_usd 窗口。
	store := quota.NewPostgresStore(pool)
	scopeID := strconv.FormatInt(f.userA, 10)
	costOnly, err := store.ListCurrentWindowsForScope(ctx, f.tenantA, quota.ScopeUser, scopeID, at)
	if err != nil {
		t.Fatalf("cost-only read: %v", err)
	}
	if len(costOnly) != 1 || costOnly[0].Metric != quota.MetricCostUSD {
		t.Fatalf("cost-only method returned %d windows (%v); must stay cost_usd-only", len(costOnly), metricsOf(costOnly))
	}
	multi, err := store.ListCurrentWindowsForScopeMetrics(ctx, f.tenantA, quota.ScopeUser, scopeID, at,
		[]quota.Metric{quota.MetricRequests, quota.MetricCostUSD, quota.MetricTokensEstimated})
	if err != nil {
		t.Fatalf("multi-metric read: %v", err)
	}
	if len(multi) != 3 {
		t.Fatalf("multi-metric method returned %d windows want 3 (%v)", len(multi), metricsOf(multi))
	}
}

// TestQuotaCostOnlyMethod_APIKeyScopeStaysCostOnly 在涉及金钱的 api_key scope 上
// 钉住「不能偏移」不变量。key-control 的 UsedUSD
// (userkeycontrols/key_control_service.go)以 ScopeAPIKey 调用
// ListCurrentWindowsForScope,并对返回的每个窗口求 settled+reserved 之和,
// 且 Go 侧没有 metric 过滤 —— 因此一旦 cost-only 方法放宽到非 cost 的 metric,
// UsedUSD 就会被污染(一个 request 计数 + 一个 token 计数被加进 USD)。
// 上面多 metric 测试里的 ScopeUser 守卫覆盖不到这个 scope。
// 变异:在 pg_store_window_reads.go 中把 cost-only 的 metric 集合放宽到包含
//
//	requests/tokens -> 在 api_key scope 这里会返回 3 个窗口 -> 转红。
func TestQuotaCostOnlyMethod_APIKeyScopeStaysCostOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := openMeQuotaPool(t, ctx)
	f := newMeQuotaFixture(t, ctx, pool)
	at := time.Now().UTC()
	// quota 的 scope_id 是自由文本,且对 api_key 而言 normalizeScopeID 是恒等
	// 映射,因此任意的 key id 都无需真实的 api_keys 行。在这个 scope 上预置全部
	// 三个 metric。
	const keyScope = "777"
	f.seedScopeQuotaWindowMetric(f.tenantA, "api_key", keyScope, "requests", at, "1000", "0", "120")
	f.seedScopeQuotaWindowMetric(f.tenantA, "api_key", keyScope, "cost_usd", at, "10", "0", "3.5")
	f.seedScopeQuotaWindowMetric(f.tenantA, "api_key", keyScope, "tokens_estimated", at, "1000000", "0", "45000")

	store := quota.NewPostgresStore(pool)
	costOnly, err := store.ListCurrentWindowsForScope(ctx, f.tenantA, quota.ScopeAPIKey, keyScope, at)
	if err != nil {
		t.Fatalf("cost-only read at api_key scope: %v", err)
	}
	if len(costOnly) != 1 || costOnly[0].Metric != quota.MetricCostUSD {
		t.Fatalf("cost-only method at api_key scope returned %d windows (%v); key-control UsedUSD must see cost_usd ONLY", len(costOnly), metricsOf(costOnly))
	}
	// 唯一的窗口是 cost 行(settled 3.5),而非计数行 —— 证明没有 metric 混入。
	if costOnly[0].SettledValue.String() != "3.5" {
		t.Fatalf("cost window settled=%s want 3.5 (a count row would be 120/45000)", costOnly[0].SettledValue.String())
	}
}

func metricsOf(rows []quota.CurrentWindowRead) []quota.Metric {
	out := make([]quota.Metric, len(rows))
	for i, r := range rows {
		out[i] = r.Metric
	}
	return out
}
