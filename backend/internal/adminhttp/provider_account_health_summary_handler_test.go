package adminhttp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/admintest"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// buildHealthSummary 是纯聚合:验证 total/enabled/disabled/needs_attention 与状态计数。
func TestBuildHealthSummaryAggregates(t *testing.T) {
	got := buildHealthSummary([]admindb.SummarizeProviderAccountHealthRow{
		{HealthState: "healthy", Enabled: true, N: 10},
		{HealthState: "healthy", Enabled: false, N: 2}, // 停用的健康账号也需关注
		{HealthState: "throttled", Enabled: true, N: 3},
		{HealthState: "revoked", Enabled: true, N: 1},
	})
	if got.Total != 16 {
		t.Fatalf("total=%d want 16", got.Total)
	}
	if got.Enabled != 14 || got.Disabled != 2 {
		t.Fatalf("enabled=%d disabled=%d want 14/2", got.Enabled, got.Disabled)
	}
	// needs_attention = 停用2 + throttled3 + revoked1 = 6(变异:若只数非healthy会漏掉停用的healthy)
	if got.NeedsAttention != 6 {
		t.Fatalf("needs_attention=%d want 6", got.NeedsAttention)
	}
	// healthy 汇总跨 enabled/disabled = 12
	byState := map[string]int64{}
	for _, s := range got.States {
		byState[s.HealthState] = s.Count
	}
	if byState["healthy"] != 12 || byState["throttled"] != 3 || byState["revoked"] != 1 {
		t.Fatalf("states=%+v want healthy12/throttled3/revoked1", got.States)
	}
	// 已知健康态固定顺序:healthy 在 throttled 前
	if got.States[0].HealthState != "healthy" {
		t.Fatalf("states[0]=%s want healthy(固定顺序)", got.States[0].HealthState)
	}
}

func TestHealthSummaryTenantScoped(t *testing.T) {
	store := newProviderAccountHealthStoreStub()
	store.summaryRows = []admindb.SummarizeProviderAccountHealthRow{{HealthState: "operational", Enabled: true, N: 5}}
	deps := ProviderAccountHealthDeps{
		Auth:  providerAccountHealthAuthStub{ident: admintest.TenantOperator(0, 7)},
		Store: store,
	}
	rec := invokeHealthSummary(t, deps, "/admin/v1/provider-accounts/health-summary?tenant_id=999")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	// 破坏点→删除 CanActOnTenant 时会以 999 查询汇总，本断言与零调用断言转红。
	if len(store.summaryArgs) != 0 {
		t.Fatalf("scope 拒绝后不应查询汇总，summaryArgs=%v", store.summaryArgs)
	}
}

func TestHealthSummaryUnauthorized(t *testing.T) {
	store := newProviderAccountHealthStoreStub()
	rec := invokeHealthSummary(t, ProviderAccountHealthDeps{
		Auth:  providerAccountHealthAuthStub{err: admin.ErrAdminUnauthorized},
		Store: store,
	}, "/admin/v1/provider-accounts/health-summary")
	if rec.Code == http.StatusOK {
		t.Fatalf("未鉴权应拒绝, code=%d", rec.Code)
	}
	if len(store.summaryArgs) != 0 {
		t.Fatalf("未鉴权不应触达 store: %v", store.summaryArgs)
	}
}

func invokeHealthSummary(t *testing.T, deps ProviderAccountHealthDeps, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/admin/v1/provider-accounts", func(r chi.Router) {
		MountProviderAccountHealthRoutes(r, deps)
	})
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}
